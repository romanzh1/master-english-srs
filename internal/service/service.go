package service

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/romanzh1/master-english-srs/internal/models"
	"github.com/romanzh1/master-english-srs/internal/service/srs"
	"github.com/romanzh1/master-english-srs/pkg/onenote"
	"github.com/romanzh1/master-english-srs/pkg/utils"
	"go.uber.org/zap"
)

type Service struct {
	repo          models.Repository
	authService   *onenote.AuthService
	oneNoteClient *onenote.Client
}

func NewService(repo models.Repository, authService *onenote.AuthService, oneNoteClient *onenote.Client) *Service {
	return &Service{
		repo:          repo,
		authService:   authService,
		oneNoteClient: oneNoteClient,
	}
}

func (s *Service) RegisterUser(ctx context.Context, telegramID int64, username, level string) error {
	exists, err := s.repo.UserExists(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("check user exists (telegram_id: %d): %w", telegramID, err)
	}

	if exists {
		return fmt.Errorf("user already exists (telegram_id: %d)", telegramID)
	}

	defaultTimezone := "UTC"
	user := &models.User{
		TelegramID:     telegramID,
		Username:       username,
		Level:          level,
		UseManualPages: false,
		ReminderTime:   "09:00",
		CreatedAt:      utils.NowUTC(),
		Timezone:       &defaultTimezone,
	}

	maxPagesPerDay := uint(2)
	if user.MaxPagesPerDay != nil {
		user.MaxPagesPerDay = &maxPagesPerDay
	}

	if err := s.repo.CreateUser(ctx, user); err != nil {
		return fmt.Errorf("create user (telegram_id: %d, username: %s): %w", telegramID, username, err)
	}

	return nil
}

func (s *Service) GetUser(ctx context.Context, telegramID int64) (*models.User, error) {
	return s.repo.GetUser(ctx, telegramID)
}

func (s *Service) UserExists(ctx context.Context, telegramID int64) (bool, error) {
	return s.repo.UserExists(ctx, telegramID)
}

func (s *Service) UpdateUserLevel(ctx context.Context, telegramID int64, level string) error {
	return s.repo.UpdateUserLevel(ctx, telegramID, level)
}

func (s *Service) GetAuthURL(telegramID int64) string {
	state := fmt.Sprintf("user_%d", telegramID)
	return s.authService.GetAuthURL(state)
}

func (s *Service) ExchangeAuthCode(ctx context.Context, telegramID int64, code string) error {
	tokenResp, err := s.authService.ExchangeCode(code)
	if err != nil {
		return fmt.Errorf("exchange auth code (telegram_id: %d): %w", telegramID, err)
	}

	auth := &models.OneNoteAuth{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    utils.NowUTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}

	if err := s.repo.UpdateOneNoteAuth(ctx, telegramID, auth); err != nil {
		return fmt.Errorf("update OneNote auth (telegram_id: %d): %w", telegramID, err)
	}

	// Сохраняем код авторизации для последующего использования
	if err := s.repo.UpdateAuthCode(ctx, telegramID, code); err != nil {
		zap.S().Warn("failed to save auth code", zap.Error(err), zap.Int64("telegram_id", telegramID))
	}

	return nil
}

// getValidAccessToken получает валидный access token, автоматически обновляя его при необходимости
// Возвращает ошибку AuthRequiredError, если требуется повторная авторизация пользователя
func (s *Service) getValidAccessToken(ctx context.Context, telegramID int64) (string, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return "", fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	// Если нет токенов, нужна авторизация
	if user.AccessToken == nil || user.RefreshToken == nil || user.ExpiresAt == nil {
		return "", &AuthRequiredError{TelegramID: telegramID}
	}

	// Проверяем, не истёк ли токен (с запасом в 5 минут)
	expiresAt := *user.ExpiresAt
	if time.Until(expiresAt) > 5*time.Minute {
		return *user.AccessToken, nil
	}

	// Токен истёк или скоро истечёт, пытаемся обновить через refresh token
	zap.S().Info("access token expired or about to expire, refreshing", zap.Int64("telegram_id", telegramID))
	tokenResp, err := s.authService.RefreshToken(*user.RefreshToken)
	if err != nil {
		zap.S().Warn("failed to refresh token", zap.Error(err), zap.Int64("telegram_id", telegramID))
		return s.tryRefreshWithAuthCode(ctx, telegramID, user)
	}

	// Обновляем токены в БД
	return s.updateTokens(ctx, telegramID, tokenResp)
}

// tryRefreshWithAuthCode пытается обновить токены через auth code, если refresh token не сработал
func (s *Service) tryRefreshWithAuthCode(ctx context.Context, telegramID int64, user *models.User) (string, error) {
	if user.AuthCode == nil || *user.AuthCode == "" {
		return "", &AuthRequiredError{TelegramID: telegramID}
	}

	zap.S().Info("trying to exchange auth code", zap.Int64("telegram_id", telegramID))
	if err := s.ExchangeAuthCode(ctx, telegramID, *user.AuthCode); err != nil {
		zap.S().Error("failed to exchange auth code", zap.Error(err), zap.Int64("telegram_id", telegramID))
		return "", &AuthRequiredError{TelegramID: telegramID}
	}

	// Получаем обновлённого пользователя
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return "", fmt.Errorf("get user after exchange (telegram_id: %d): %w", telegramID, err)
	}

	if user.AccessToken == nil {
		return "", &AuthRequiredError{TelegramID: telegramID}
	}

	return *user.AccessToken, nil
}

// updateTokens обновляет токены в БД
func (s *Service) updateTokens(ctx context.Context, telegramID int64, tokenResp *onenote.TokenResponse) (string, error) {
	auth := &models.OneNoteAuth{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    utils.NowUTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}

	if err := s.repo.UpdateOneNoteAuth(ctx, telegramID, auth); err != nil {
		return "", fmt.Errorf("update OneNote auth after refresh (telegram_id: %d): %w", telegramID, err)
	}

	return tokenResp.AccessToken, nil
}

// AuthRequiredError указывает, что требуется повторная авторизация пользователя
type AuthRequiredError struct {
	TelegramID int64
}

func (e *AuthRequiredError) Error() string {
	return fmt.Sprintf("authentication required for user %d", e.TelegramID)
}

func (s *Service) GetOneNoteNotebooks(ctx context.Context, telegramID int64) ([]onenote.Notebook, error) {
	var notebooks []onenote.Notebook

	err := s.withAuthRetry(ctx, telegramID, func(accessToken string) error {
		result, err := s.oneNoteClient.GetNotebooks(accessToken)
		if err != nil {
			return fmt.Errorf("get notebooks (telegram_id: %d): %w", telegramID, err)
		}
		notebooks = result
		return nil
	})

	return notebooks, err
}

func (s *Service) GetOneNoteSections(ctx context.Context, telegramID int64, notebookID string) ([]onenote.Section, error) {
	var sections []onenote.Section

	err := s.withAuthRetry(ctx, telegramID, func(accessToken string) error {
		result, err := s.oneNoteClient.GetSections(accessToken, notebookID)
		if err != nil {
			return fmt.Errorf("get sections (telegram_id: %d, notebook_id: %s): %w", telegramID, notebookID, err)
		}
		sections = result
		return nil
	})

	return sections, err
}

func (s *Service) SaveOneNoteConfig(ctx context.Context, telegramID int64, notebookID, sectionID string) error {
	config := &models.OneNoteConfig{
		NotebookID: notebookID,
		SectionID:  sectionID,
	}

	if err := s.repo.UpdateOneNoteConfig(ctx, telegramID, config); err != nil {
		return fmt.Errorf("save OneNote config (telegram_id: %d, notebook_id: %s): %w", telegramID, notebookID, err)
	}

	return nil
}

func (s *Service) syncPagesInternal(ctx context.Context, telegramID int64) error {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteConfig == nil {
		return fmt.Errorf("onenote not configured (telegram_id: %d)", telegramID)
	}

	var pages []onenote.Page
	err = s.withAuthRetry(ctx, telegramID, func(accessToken string) error {
		result, err := s.oneNoteClient.GetPages(accessToken, user.OneNoteConfig.SectionID)
		if err != nil {
			return fmt.Errorf("get pages (telegram_id: %d, section_id: %s): %w", telegramID, user.OneNoteConfig.SectionID, err)
		}
		pages = result
		return nil
	})

	if err != nil {
		return err
	}

	for _, page := range pages {
		if strings.Contains(page.Title, "*") || !hasPageNumber(page.Title) {
			continue
		}

		var updatedAt *time.Time
		if page.LastModifiedDateTime != "" {
			parsed, err := time.Parse(time.RFC3339, page.LastModifiedDateTime)
			if err == nil {
				updatedAt = &parsed
			}
		}

		pageRef := &models.PageReference{
			PageID:    page.ID,
			UserID:    telegramID,
			Title:     page.Title,
			Source:    "onenote",
			CreatedAt: utils.NowUTC(),
			UpdatedAt: updatedAt,
		}

		if err := s.repo.UpsertPageReference(ctx, pageRef); err != nil {
			zap.S().Error("upsert page reference", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", page.ID))
			continue
		}
	}

	return nil
}

// withAuthRetry выполняет операцию OneNote API с автоматической обработкой ошибок авторизации
// Если операция возвращает ошибку авторизации, обновляет токен и повторяет операцию
func (s *Service) withAuthRetry(ctx context.Context, telegramID int64, operation func(string) error) error {
	accessToken, err := s.getValidAccessToken(ctx, telegramID)
	if err != nil {
		return err
	}

	err = operation(accessToken)
	if err == nil {
		return nil
	}

	// Если ошибка авторизации, пытаемся обновить токен и повторить
	if !isAuthError(err) {
		return err
	}

	// Получаем новый токен и повторяем операцию
	accessToken, err = s.getValidAccessToken(ctx, telegramID)
	if err != nil {
		return err
	}

	return operation(accessToken)
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "status: 401") || strings.Contains(errStr, "status: 403")
}

func (s *Service) GetDuePagesToday(ctx context.Context, telegramID int64) ([]*models.PageWithProgress, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteConfig == nil {
		return nil, fmt.Errorf("onenote not configured (telegram_id: %d)", telegramID)
	}

	timezone := "UTC"
	if user.Timezone != nil && *user.Timezone != "" {
		timezone = *user.Timezone
	}

	// Calculate end of day in user's timezone, then convert to UTC for database query
	startOfDayInTz, err := utils.StartOfTodayInTimezone(timezone)
	if err != nil {
		return nil, fmt.Errorf("get start of day in timezone (telegram_id: %d, timezone: %s): %w", telegramID, timezone, err)
	}

	endOfDayUTC := startOfDayInTz.AddDate(0, 0, 1).UTC()

	progressList, err := s.repo.GetDuePagesToday(ctx, telegramID, endOfDayUTC)
	if err != nil {
		return nil, fmt.Errorf("get due pages today (telegram_id: %d): %w", telegramID, err)
	}

	if len(progressList) == 0 {
		return []*models.PageWithProgress{}, nil
	}

	var onenotePages []onenote.Page
	err = s.withAuthRetry(ctx, telegramID, func(accessToken string) error {
		result, err := s.oneNoteClient.GetPages(accessToken, user.OneNoteConfig.SectionID)
		if err != nil {
			return fmt.Errorf("get pages (telegram_id: %d, section_id: %s): %w", telegramID, user.OneNoteConfig.SectionID, err)
		}
		onenotePages = result
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("get onenote pages (telegram_id: %d): %w", telegramID, err)
	}

	pageMap := make(map[string]onenote.Page, len(onenotePages))
	for _, page := range onenotePages {
		pageMap[page.ID] = page
	}

	result := make([]*models.PageWithProgress, 0, len(progressList))
	for _, progress := range progressList {
		page, ok := pageMap[progress.PageID]
		if !ok {
			continue
		}

		if strings.Contains(page.Title, "*") || !hasPageNumber(page.Title) {
			continue
		}

		var updatedAt *time.Time
		if page.LastModifiedDateTime != "" {
			parsed, err := time.Parse(time.RFC3339, page.LastModifiedDateTime)
			if err == nil {
				updatedAt = &parsed
			}
		}

		pwp := &models.PageWithProgress{
			Page: models.PageReference{
				PageID:    page.ID,
				UserID:    telegramID,
				Title:     page.Title,
				Source:    "onenote",
				CreatedAt: time.Now(),
				UpdatedAt: updatedAt,
			},
			Progress: progress,
		}
		result = append(result, pwp)
	}

	slices.SortFunc(result, func(a, b *models.PageWithProgress) int {
		numA := extractPageNumber(a.Page.Title)
		numB := extractPageNumber(b.Page.Title)
		if numA != numB {
			return cmp.Compare(numA, numB)
		}

		return a.Progress.NextReviewDate.Compare(b.Progress.NextReviewDate)
	})

	return result, nil
}

func (s *Service) GetUserAllPagesInProgress(ctx context.Context, telegramID int64) ([]*models.PageReference, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteConfig == nil {
		return nil, fmt.Errorf("onenote not configured (telegram_id: %d)", telegramID)
	}

	var onenotePages []onenote.Page
	err = s.withAuthRetry(ctx, telegramID, func(accessToken string) error {
		result, err := s.oneNoteClient.GetPages(accessToken, user.OneNoteConfig.SectionID)
		if err != nil {
			return fmt.Errorf("get pages (telegram_id: %d, section_id: %s): %w", telegramID, user.OneNoteConfig.SectionID, err)
		}
		onenotePages = result
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("get onenote pages (telegram_id: %d): %w", telegramID, err)
	}

	allProgressPageIDs, err := s.repo.GetAllProgressPageIDs(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("get all progress page IDs (telegram_id: %d): %w", telegramID, err)
	}

	inProgressMap := make(map[string]bool, len(allProgressPageIDs))
	for _, pageID := range allProgressPageIDs {
		inProgressMap[pageID] = true
	}

	result := make([]*models.PageReference, 0, len(onenotePages))
	for _, page := range onenotePages {
		if page.ID == "" {
			continue
		}

		if strings.Contains(page.Title, "*") || !hasPageNumber(page.Title) {
			continue
		}

		if !inProgressMap[page.ID] {
			continue
		}

		var updatedAt *time.Time
		if page.LastModifiedDateTime != "" {
			parsed, err := time.Parse(time.RFC3339, page.LastModifiedDateTime)
			if err == nil {
				updatedAt = &parsed
			}
		}

		pageRef := &models.PageReference{
			PageID:    page.ID,
			UserID:    telegramID,
			Title:     page.Title,
			Source:    "onenote",
			CreatedAt: utils.NowUTC(),
			UpdatedAt: updatedAt,
		}

		if err := s.repo.UpsertPageReference(ctx, pageRef); err != nil {
			zap.S().Error("upsert page reference", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", page.ID))
			// Продолжаем, даже если не удалось сохранить в БД
		}

		result = append(result, pageRef)
	}

	slices.SortFunc(result, func(a, b *models.PageReference) int {
		numA := extractPageNumber(a.Title)
		numB := extractPageNumber(b.Title)
		return cmp.Compare(numA, numB)
	})

	return result, nil
}

// hasPageNumber проверяет, содержит ли заголовок номер страницы в начале
// Возвращает true, если в начале title есть число
func hasPageNumber(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}

	// Ищем первое число в начале строки
	re := regexp.MustCompile(`^\d+`)
	match := re.FindString(title)
	return match != ""
}

// extractPageNumber извлекает первое число из начала заголовка страницы
// Например, "14 Grammar Sequence of Tenses" -> 14
// Если число не найдено, возвращает максимальное значение int для сортировки в конец
func extractPageNumber(title string) int {
	title = strings.TrimSpace(title)
	if title == "" {
		return 0
	}

	// Ищем первое число в начале строки
	re := regexp.MustCompile(`^\d+`)
	match := re.FindString(title)
	if match == "" {
		return 999999
	}

	num, err := strconv.Atoi(match)
	if err != nil {
		return 999999
	}

	return num
}

func (s *Service) GetPageContent(ctx context.Context, telegramID int64, pageID string) (string, error) {
	var content string

	err := s.withAuthRetry(ctx, telegramID, func(accessToken string) error {
		result, err := s.oneNoteClient.GetPageContent(accessToken, pageID)
		if err != nil {
			return fmt.Errorf("get page content (telegram_id: %d, page_id: %s): %w", telegramID, pageID, err)
		}
		content = result
		return nil
	})

	return content, err
}

func (s *Service) UpdateReviewProgress(ctx context.Context, telegramID int64, pageID string, grade int) error {
	progress, err := s.repo.GetProgress(ctx, telegramID, pageID)
	if err != nil {
		return fmt.Errorf("get progress (telegram_id: %d, page_id: %s): %w", telegramID, pageID, err)
	}

	// Проверяем, приостановлен ли пользователь, чтобы возобновить его при активности
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	status := srs.ConvertGradeToStatus(grade)

	timezone := "UTC"
	if user.Timezone != nil && *user.Timezone != "" {
		timezone = *user.Timezone
	}

	var nextReview time.Time
	var newInterval int
	var historyMode string

	// Режим чтения (IntervalDays == 0): пользователь только читает слова
	if progress.IntervalDays == 0 {
		statusStr := string(status)
		if statusStr == "normal" || statusStr == "easy" {
			// Пользователь помнит слова → переход к AI режиму завтра
			nextReview, newInterval = srs.GetNextDayReviewDate(timezone)
		} else {
			// Пользователь не помнит слова → остаёмся в режиме чтения, повтор завтра
			nextReview, newInterval = srs.GetNextDayReadingMode(timezone)
		}
		historyMode = "reading"
	} else {
		// AI режим: стандартные SRS интервалы
		nextReview, newInterval = srs.CalculateNextReviewDate(progress.IntervalDays, status, timezone)
		historyMode = "standard"
	}

	// Определяем флаг passed: страница считается изученной, если она уже была на последнем интервале (180 дней)
	// и при текущем прохождении статус = "easy" или "normal" (успешное прохождение последнего интервала)
	passed := false
	statusStr := string(status)
	if progress.IntervalDays == 180 && (statusStr == "easy" || statusStr == "normal") {
		passed = true
	}

	nowUTC := utils.NowUTC()
	history := models.ProgressHistory{
		Date:  nowUTC,
		Score: grade,
		Mode:  historyMode,
		Notes: "",
	}

	if err := s.repo.UpdateProgress(ctx, telegramID, pageID, progress.Level, progress.RepetitionCount, nowUTC, nextReview, newInterval, true, passed); err != nil {
		return fmt.Errorf("update progress (telegram_id: %d, page_id: %s): %w", telegramID, pageID, err)
	}

	if err := s.repo.AddProgressHistory(ctx, telegramID, pageID, history); err != nil {
		zap.S().Error("add progress history", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", pageID))
	}

	if err := s.repo.UpdateUserActivity(ctx, telegramID, nowUTC); err != nil {
		zap.S().Error("update user activity", zap.Error(err), zap.Int64("telegram_id", telegramID))
	}

	// Если пользователь был приостановлен, возобновляем его
	if user.IsPaused != nil && *user.IsPaused {
		if err := s.resumeUserOnActivity(ctx, telegramID); err != nil {
			zap.S().Error("resume user on activity", zap.Error(err), zap.Int64("telegram_id", telegramID))
		}
	}

	return nil
}

func (s *Service) GetAllUsersForReminders(ctx context.Context) ([]*models.User, error) {
	return s.repo.GetAllUsersWithReminders(ctx)
}

func (s *Service) GetProgress(ctx context.Context, telegramID int64, pageID string) (*models.UserProgress, error) {
	return s.repo.GetProgress(ctx, telegramID, pageID)
}

func (s *Service) GetLastReviewScore(ctx context.Context, telegramID int64, pageID string) (int, error) {
	return s.repo.GetLastReviewScore(ctx, telegramID, pageID)
}

func (s *Service) UpdateMaxPagesPerDay(ctx context.Context, telegramID int64, maxPages uint) error {
	if err := s.repo.UpdateMaxPagesPerDay(ctx, telegramID, maxPages); err != nil {
		return fmt.Errorf("update max pages per day (telegram_id: %d, max_pages: %d): %w", telegramID, maxPages, err)
	}

	return nil
}

func (s *Service) UpdateUserTimezone(ctx context.Context, telegramID int64, timezone string) error {
	if err := s.repo.UpdateUserTimezone(ctx, telegramID, timezone); err != nil {
		return fmt.Errorf("update user timezone (telegram_id: %d, timezone: %s): %w", telegramID, timezone, err)
	}

	return nil
}

func (s *Service) addPagesToLearning(ctx context.Context, telegramID int64) error {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteConfig == nil {
		return fmt.Errorf("onenote not configured (telegram_id: %d)", telegramID)
	}

	// Проверяем, приостановлен ли пользователь
	if user.IsPaused != nil && *user.IsPaused {
		zap.S().Info("user is paused, skipping add pages to learning", zap.Int64("telegram_id", telegramID))
		return nil
	}

	maxPagesPerDay := uint(2)
	if user.MaxPagesPerDay != nil {
		maxPagesPerDay = *user.MaxPagesPerDay
	}

	// Получаем timezone пользователя
	timezone := "UTC"
	if user.Timezone != nil && *user.Timezone != "" {
		timezone = *user.Timezone
	}

	// Calculate end of day in user's timezone, then convert to UTC for database query
	var endOfDayUTC time.Time
	startOfDayInTz, err := utils.StartOfTodayInTimezone(timezone)
	if err != nil {
		zap.S().Error("get start of day in timezone", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("timezone", timezone))
		endOfDayUTC = utils.StartOfTodayUTC().AddDate(0, 0, 1)
	} else {
		endOfDayUTC = startOfDayInTz.AddDate(0, 0, 1).UTC()
	}

	duePagesToday, err := s.repo.GetDuePagesToday(ctx, telegramID, endOfDayUTC)
	if err != nil {
		zap.S().Error("get due pages today", zap.Error(err), zap.Int64("telegram_id", telegramID))
		// Продолжаем, если ошибка подсчёта
	} else if uint(len(duePagesToday)) >= maxPagesPerDay {
		zap.S().Info("due pages today >= max pages per day, skipping add pages", zap.Int64("telegram_id", telegramID), zap.Int("due_pages_today", len(duePagesToday)), zap.Uint("max_pages_per_day", maxPagesPerDay))
		return nil
	}

	pagesToAdd := srs.CalculatePagesToAdd(maxPagesPerDay)

	var onenotePages []onenote.Page
	err = s.withAuthRetry(ctx, telegramID, func(accessToken string) error {
		result, err := s.oneNoteClient.GetPages(accessToken, user.OneNoteConfig.SectionID)
		if err != nil {
			return fmt.Errorf("get pages (telegram_id: %d, section_id: %s): %w", telegramID, user.OneNoteConfig.SectionID, err)
		}

		onenotePages = result

		return nil
	})
	if err != nil {
		return fmt.Errorf("get onenote pages (telegram_id: %d): %w", telegramID, err)
	}

	availablePages := make([]onenote.Page, 0, len(onenotePages))
	for _, page := range onenotePages {
		if !strings.Contains(page.Title, "*") && hasPageNumber(page.Title) {
			availablePages = append(availablePages, page)
		}
	}

	slices.SortFunc(availablePages, func(a, b onenote.Page) int {
		numA := extractPageNumber(a.Title)
		numB := extractPageNumber(b.Title)
		return cmp.Compare(numA, numB)
	})

	pageIDs := make([]string, 0, len(availablePages))
	for _, page := range availablePages {
		pageIDs = append(pageIDs, page.ID)
	}

	notInProgress, err := s.repo.GetPageIDsNotInProgress(ctx, telegramID, pageIDs)
	if err != nil {
		return fmt.Errorf("get page IDs not in progress (telegram_id: %d): %w", telegramID, err)
	}

	if len(notInProgress) > pagesToAdd {
		notInProgress = notInProgress[:pagesToAdd]
	}

	err = s.repo.RunInTx(ctx, func(txRepo models.Repository) error {
		for _, pageID := range notInProgress {
			nextReview, interval := srs.GetInitialReviewDate(timezone)
			progress := &models.UserProgress{
				UserID:          telegramID,
				PageID:          pageID,
				Level:           user.Level,
				RepetitionCount: 0,
				LastReviewDate:  utils.NowUTC(),
				NextReviewDate:  nextReview,
				IntervalDays:    interval,
				SuccessRate:     0,
				ReviewedToday:   false,
				Passed:          false,
			}

			if err := txRepo.CreateProgress(ctx, progress); err != nil {
				zap.S().Error("create progress in tx", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", pageID))
				return fmt.Errorf("create progress in tx: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("run in transaction: %w", err)
	}

	return nil
}

func (s *Service) PrepareMaterials(ctx context.Context, telegramID int64) error {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteConfig == nil {
		return fmt.Errorf("onenote not configured (telegram_id: %d)", telegramID)
	}

	err = s.syncPagesInternal(ctx, telegramID)
	if err != nil {
		if _, ok := err.(*AuthRequiredError); ok {
			return err
		}
		zap.S().Warn("failed to sync pages in prepare materials", zap.Error(err), zap.Int64("telegram_id", telegramID))
	}

	if err := s.addPagesToLearning(ctx, telegramID); err != nil {
		return fmt.Errorf("add pages to learning: %w", err)
	}

	return nil
}

func (s *Service) SkipPage(ctx context.Context, userID int64, pageID string) error {
	if err := s.repo.DeleteProgress(ctx, userID, pageID); err != nil {
		return fmt.Errorf("delete progress (telegram_id: %d, page_id: %s): %w", userID, pageID, err)
	}
	return nil
}

func (s *Service) RunDailyCron(ctx context.Context) error {
	zap.S().Info("running daily cron")

	users, err := s.repo.GetAllUsersWithReminders(ctx)
	if err != nil {
		return fmt.Errorf("get all users: %w", err)
	}

	for _, user := range users {
		if user.OneNoteConfig == nil {
			continue
		}

		// Проверяем, приостановлен ли пользователь
		if user.IsPaused != nil && *user.IsPaused {
			zap.S().Info("skipping paused user in daily cron", zap.Int64("telegram_id", user.TelegramID))
			continue
		}

		// Определяем таймзону пользователя
		timezone := "UTC"
		if user.Timezone != nil && *user.Timezone != "" {
			timezone = *user.Timezone
		}

		// Вычисляем начало сегодняшнего дня в таймзоне пользователя и конвертируем в UTC
		startOfTodayInTz, err := utils.StartOfTodayInTimezone(timezone)
		if err != nil {
			zap.S().Warn("failed to get start of today in timezone", zap.Error(err), zap.Int64("telegram_id", user.TelegramID), zap.String("timezone", timezone))
			continue
		}
		startOfTodayUTC := startOfTodayInTz.UTC()

		processed, err := s.repo.TryProcessDailyCronForUser(ctx, user.TelegramID, startOfTodayUTC)
		if err != nil {
			zap.S().Error("try process daily cron for user", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
			continue
		}

		if !processed {
			continue
		}

		err = s.syncPagesInternal(ctx, user.TelegramID)
		if err != nil {
			if _, ok := err.(*AuthRequiredError); ok {
				zap.S().Warn("auth required for daily cron", zap.Int64("telegram_id", user.TelegramID))
				continue
			}
			zap.S().Warn("failed to sync pages in daily cron", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
		}

		if err := s.addPagesToLearning(ctx, user.TelegramID); err != nil {
			zap.S().Error("add pages to learning in daily cron", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
			continue
		}

		if err := s.repo.ResetReviewedTodayFlag(ctx, user.TelegramID); err != nil {
			zap.S().Error("reset reviewed today flag in daily cron", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
		}
	}

	// Проверяем пользователей без активности неделю и приостанавливаем их
	if err := s.checkAndPauseInactiveUsers(ctx); err != nil {
		zap.S().Error("check and pause inactive users", zap.Error(err))
	}

	// Проверяем пользователей без активности месяц и сбрасываем интервалы
	if err := s.checkAndResetIntervals(ctx); err != nil {
		zap.S().Error("check and reset intervals", zap.Error(err))
	}

	return nil
}

func (s *Service) checkAndPauseInactiveUsers(ctx context.Context) error {
	weekAgo := utils.NowUTC().AddDate(0, 0, -7)
	users, err := s.repo.GetUsersWithoutActivityAfter(ctx, weekAgo, true)
	if err != nil {
		return fmt.Errorf("get users without activity for week: %w", err)
	}

	for _, user := range users {
		if user.OneNoteConfig == nil {
			continue
		}

		maxPagesPerDay := uint(2)
		if user.MaxPagesPerDay != nil {
			maxPagesPerDay = *user.MaxPagesPerDay
		}

		timezone := "UTC"
		if user.Timezone != nil && *user.Timezone != "" {
			timezone = *user.Timezone
		}

		// Calculate end of day in user's timezone, then convert to UTC for database query
		var endOfDayUTC time.Time
		startOfDayInTz, err := utils.StartOfTodayInTimezone(timezone)
		if err != nil {
			zap.S().Error("get start of day in timezone", zap.Error(err), zap.Int64("telegram_id", user.TelegramID), zap.String("timezone", timezone))
			endOfDayUTC = utils.StartOfTodayUTC().AddDate(0, 0, 1)
		} else {
			endOfDayUTC = startOfDayInTz.AddDate(0, 0, 1).UTC()
		}

		duePagesToday, err := s.repo.GetDuePagesToday(ctx, user.TelegramID, endOfDayUTC)
		if err != nil {
			zap.S().Error("get due pages today", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
			continue
		}

		// Приостанавливаем пользователя только если он неактивен неделю И количество страниц в today достигло максимума
		if len(duePagesToday) >= int(maxPagesPerDay) {
			if err := s.repo.SetUserPaused(ctx, user.TelegramID, true); err != nil {
				zap.S().Error("set user paused", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
				continue
			}
			zap.S().Info("user paused due to inactivity and max pages in today reached", zap.Int64("telegram_id", user.TelegramID), zap.Int("due_pages_today", len(duePagesToday)), zap.Uint("max_pages_per_day", maxPagesPerDay))
		}
	}

	return nil
}

func (s *Service) checkAndResetIntervals(ctx context.Context) error {
	monthAgo := utils.NowUTC().AddDate(0, 0, -30)
	users, err := s.repo.GetUsersWithoutActivityAfter(ctx, monthAgo, false)
	if err != nil {
		return fmt.Errorf("get users without activity for month: %w", err)
	}

	today := utils.StartOfTodayUTC()
	tomorrowUTC := today.AddDate(0, 0, 1)
	monthFromNowUTC := today.AddDate(0, 0, 30)

	for _, user := range users {
		if err := s.repo.ResetIntervalForPagesDueInMonth(ctx, user.TelegramID, tomorrowUTC, monthFromNowUTC); err != nil {
			zap.S().Error("reset interval for pages due in month", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
			continue
		}
		zap.S().Info("intervals reset for inactive user", zap.Int64("telegram_id", user.TelegramID))
	}

	return nil
}

func (s *Service) resumeUserOnActivity(ctx context.Context, userID int64) error {
	if err := s.repo.SetUserPaused(ctx, userID, false); err != nil {
		return fmt.Errorf("resume user on activity (telegram_id: %d): %w", userID, err)
	}
	zap.S().Info("user resumed due to activity", zap.Int64("telegram_id", userID))
	return nil
}
