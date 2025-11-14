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

type Repository interface {
	CreateUser(ctx context.Context, user *models.User) error
	GetUser(ctx context.Context, telegramID int64) (*models.User, error)
	UserExists(ctx context.Context, telegramID int64) (bool, error)
	UpdateUserLevel(ctx context.Context, telegramID int64, level string) error
	UpdateOneNoteAuth(ctx context.Context, telegramID int64, auth *models.OneNoteAuth) error
	UpdateAuthCode(ctx context.Context, telegramID int64, authCode string) error
	UpdateOneNoteConfig(ctx context.Context, telegramID int64, config *models.OneNoteConfig) error
	UpdateMaxPagesPerDay(ctx context.Context, telegramID int64, maxPages uint) error
	GetAllUsersWithReminders(ctx context.Context) ([]*models.User, error)
	SetMaterialsPreparedAt(ctx context.Context, telegramID int64, preparedAt time.Time) error
	RunInTx(ctx context.Context, fn func(interface{}) error) error

	CreatePageReference(ctx context.Context, page *models.PageReference) error
	GetPageReference(ctx context.Context, pageID string, userID int64) (*models.PageReference, error)
	GetUserPages(ctx context.Context, userID int64) ([]*models.PageReference, error)
	DeleteUserPages(ctx context.Context, userID int64) error

	CreateProgress(ctx context.Context, progress *models.UserProgress) error
	GetProgress(ctx context.Context, userID int64, pageID string) (*models.UserProgress, error)
	UpdateProgress(ctx context.Context, userID int64, pageID string, level string, repetitionCount int, lastReviewDate, nextReviewDate time.Time, intervalDays int) error
	AddProgressHistory(ctx context.Context, userID int64, pageID string, history models.ProgressHistory) error
	GetDuePagesToday(ctx context.Context, userID int64) ([]*models.UserProgress, error)
	GetAllProgressPageIDs(ctx context.Context, userID int64) ([]string, error)
	GetPageIDsNotInProgress(ctx context.Context, userID int64, pageIDs []string) ([]string, error)
	ProgressExists(ctx context.Context, userID int64, pageID string) (bool, error)
}

type Service struct {
	repo          Repository
	authService   *onenote.AuthService
	oneNoteClient *onenote.Client
}

func NewService(repo Repository, authService *onenote.AuthService, oneNoteClient *onenote.Client) *Service {
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

	user := &models.User{
		TelegramID:     telegramID,
		Username:       username,
		Level:          level,
		UseManualPages: false,
		ReminderTime:   "09:00",
		CreatedAt:      time.Now(),
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
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
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
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
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

func (s *Service) syncPagesInternal(ctx context.Context, telegramID int64) (int, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return 0, fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteConfig == nil {
		return 0, fmt.Errorf("onenote not configured (telegram_id: %d)", telegramID)
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
		return 0, err
	}

	if err := s.repo.DeleteUserPages(ctx, telegramID); err != nil {
		zap.S().Error("delete user pages", zap.Error(err), zap.Int64("telegram_id", telegramID))
	}

	for _, page := range pages {
		// Пропускаем страницы с * в заголовке (страницы на доработке)
		if strings.Contains(page.Title, "*") {
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
			CreatedAt: time.Now(),
			UpdatedAt: updatedAt,
		}

		if err := s.repo.CreatePageReference(ctx, pageRef); err != nil {
			zap.S().Error("create page reference", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", page.ID))
			continue
		}
	}

	return len(pages), nil
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

	progressList, err := s.repo.GetDuePagesToday(ctx, telegramID)
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

		if strings.Contains(page.Title, "*") {
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

func (s *Service) GetUserPages(ctx context.Context, telegramID int64) ([]*models.PageReference, error) {
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
		if strings.Contains(page.Title, "*") {
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
			CreatedAt: time.Now(),
			UpdatedAt: updatedAt,
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

	status := srs.ConvertGradeToStatus(grade)
	nextReview, newInterval := srs.CalculateNextReviewDate(progress.IntervalDays, status)

	newRepCount := progress.RepetitionCount + 1

	history := models.ProgressHistory{
		Date:  time.Now(),
		Score: grade,
		Mode:  "standard", // TODO get from model
		Notes: "",
	}

	if err := s.repo.UpdateProgress(ctx, telegramID, pageID, progress.Level, newRepCount, time.Now(), nextReview, newInterval); err != nil {
		return fmt.Errorf("update progress (telegram_id: %d, page_id: %s): %w", telegramID, pageID, err)
	}

	if err := s.repo.AddProgressHistory(ctx, telegramID, pageID, history); err != nil {
		zap.S().Error("add progress history", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", pageID))
	}

	return nil
}

func (s *Service) GetAllUsersForReminders(ctx context.Context) ([]*models.User, error) {
	return s.repo.GetAllUsersWithReminders(ctx)
}

func (s *Service) GetProgress(ctx context.Context, telegramID int64, pageID string) (*models.UserProgress, error) {
	return s.repo.GetProgress(ctx, telegramID, pageID)
}

func (s *Service) UpdateMaxPagesPerDay(ctx context.Context, telegramID int64, maxPages uint) error {
	if err := s.repo.UpdateMaxPagesPerDay(ctx, telegramID, maxPages); err != nil {
		return fmt.Errorf("update max pages per day (telegram_id: %d, max_pages: %d): %w", telegramID, maxPages, err)
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

	maxPagesPerDay := uint(2)
	if user.MaxPagesPerDay != nil {
		maxPagesPerDay = *user.MaxPagesPerDay
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
		if !strings.Contains(page.Title, "*") {
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

	preparedAt := time.Now()
	err = s.repo.RunInTx(ctx, func(txRepo interface{}) error {
		repo := txRepo.(Repository)
		for _, pageID := range notInProgress {
			nextReview, interval := srs.GetInitialReviewDate()
			progress := &models.UserProgress{
				UserID:          telegramID,
				PageID:          pageID,
				Level:           user.Level,
				RepetitionCount: 0,
				NextReviewDate:  nextReview,
				IntervalDays:    interval,
				SuccessRate:     0,
			}

			if err := repo.CreateProgress(ctx, progress); err != nil {
				zap.S().Error("create progress in tx", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", pageID))
				return fmt.Errorf("create progress in tx: %w", err)
			}
		}

		if err := repo.SetMaterialsPreparedAt(ctx, telegramID, preparedAt); err != nil {
			zap.S().Error("set materials prepared at in tx", zap.Error(err), zap.Int64("telegram_id", telegramID))
			return fmt.Errorf("set materials prepared at in tx: %w", err)
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

	_, err = s.syncPagesInternal(ctx, telegramID)
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

func (s *Service) RunDailyCron(ctx context.Context) error {
	users, err := s.repo.GetAllUsersWithReminders(ctx)
	if err != nil {
		return fmt.Errorf("get all users: %w", err)
	}

	now := utils.TruncateToMinutes(time.Now())
	today := utils.StartOfDay(now)

	for _, user := range users {
		if user.OneNoteConfig == nil {
			continue
		}

		if user.MaterialsPreparedAt != nil {
			preparedDate := utils.StartOfDay(*user.MaterialsPreparedAt)
			if utils.DatesEqual(preparedDate, today) {
				zap.S().Info("skipping user with materials already prepared today", zap.Int64("telegram_id", user.TelegramID))
				continue
			}
		}

		_, err := s.syncPagesInternal(ctx, user.TelegramID)
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
	}

	return nil
}
