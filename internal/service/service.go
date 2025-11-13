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

	"github.com/yourusername/master-english-srs/internal/models"
	"github.com/yourusername/master-english-srs/internal/srs"
	"github.com/yourusername/master-english-srs/pkg/onenote"
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
	GetAllUsersWithReminders(ctx context.Context) ([]*models.User, error)

	CreatePageReference(ctx context.Context, page *models.PageReference) error
	GetPageReference(ctx context.Context, pageID string, userID int64) (*models.PageReference, error)
	GetUserPages(ctx context.Context, userID int64) ([]*models.PageReference, error)
	DeleteUserPages(ctx context.Context, userID int64) error

	CreateProgress(ctx context.Context, progress *models.UserProgress) error
	GetProgress(ctx context.Context, userID int64, pageID string) (*models.UserProgress, error)
	UpdateProgress(ctx context.Context, userID int64, pageID string, repetitionCount int, lastReviewDate, nextReviewDate time.Time, intervalDays int) error
	AddProgressHistory(ctx context.Context, userID int64, pageID string, history models.ProgressHistory) error
	GetDuePagesToday(ctx context.Context, userID int64) ([]*models.PageWithProgress, error)
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

// syncPagesInternal выполняет синхронизацию страниц из OneNote
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

		pageRef := &models.PageReference{
			PageID:     page.ID,
			UserID:     telegramID,
			Title:      page.Title,
			Category:   "vocabulary",
			Level:      user.Level,
			Source:     "onenote",
			CreatedAt:  time.Now(),
			LastSynced: time.Now(),
		}

		if err := s.repo.CreatePageReference(ctx, pageRef); err != nil {
			zap.S().Error("create page reference", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", page.ID))
			continue
		}

		exists, err := s.repo.ProgressExists(ctx, telegramID, page.ID)
		if err != nil {
			zap.S().Error("check progress exists", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", page.ID))
			continue
		}

		if !exists {
			nextReview, interval := srs.GetInitialReviewDate()
			progress := &models.UserProgress{
				UserID:          telegramID,
				PageID:          page.ID,
				RepetitionCount: 0,
				NextReviewDate:  nextReview,
				IntervalDays:    interval,
				SuccessRate:     0,
			}

			if err := s.repo.CreateProgress(ctx, progress); err != nil {
				zap.S().Error("create progress", zap.Error(err), zap.Int64("telegram_id", telegramID), zap.String("page_id", page.ID))
			}
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

// isAuthError проверяет, является ли ошибка ошибкой авторизации (401 или 403)
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "status: 401") || strings.Contains(errStr, "status: 403")
}

func (s *Service) GetDuePagesToday(ctx context.Context, telegramID int64) ([]*models.PageWithProgress, error) {
	_, err := s.syncPagesInternal(ctx, telegramID)
	if err != nil {
		if _, ok := err.(*AuthRequiredError); ok {
			return nil, fmt.Errorf("sync pages (telegram_id: %d): %w", telegramID, err)
		}
		zap.S().Warn("failed to sync pages before getting due pages", zap.Error(err), zap.Int64("telegram_id", telegramID))
	}

	duePages, err := s.repo.GetDuePagesToday(ctx, telegramID)
	if err != nil {
		return nil, err
	}

	// Фильтруем страницы с * в заголовке (страницы на доработке)
	filteredPages := make([]*models.PageWithProgress, 0, len(duePages))
	for _, pwp := range duePages {
		if !strings.Contains(pwp.Page.Title, "*") {
			filteredPages = append(filteredPages, pwp)
		}
	}

	// Сортируем по номеру из заголовка, затем по дате следующего повторения
	slices.SortFunc(filteredPages, func(a, b *models.PageWithProgress) int {
		numA := extractPageNumber(a.Page.Title)
		numB := extractPageNumber(b.Page.Title)
		if numA != numB {
			return cmp.Compare(numA, numB)
		}
		// Если номера одинаковые, сортируем по дате следующего повторения
		return a.Progress.NextReviewDate.Compare(b.Progress.NextReviewDate)
	})

	return filteredPages, nil
}

func (s *Service) GetUserPages(ctx context.Context, telegramID int64) ([]*models.PageReference, error) {
	_, err := s.syncPagesInternal(ctx, telegramID)
	if err != nil {
		if _, ok := err.(*AuthRequiredError); ok {
			return nil, fmt.Errorf("sync pages (telegram_id: %d): %w", telegramID, err)
		}
		zap.S().Warn("failed to sync pages before getting user pages", zap.Error(err), zap.Int64("telegram_id", telegramID))
	}

	pages, err := s.repo.GetUserPages(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("get user pages (telegram_id: %d): %w", telegramID, err)
	}

	// Фильтруем страницы с * в заголовке (страницы на доработке)
	filteredPages := make([]*models.PageReference, 0, len(pages))
	for _, page := range pages {
		if !strings.Contains(page.Title, "*") {
			filteredPages = append(filteredPages, page)
		}
	}

	// Сортируем по номеру из заголовка
	slices.SortFunc(filteredPages, func(a, b *models.PageReference) int {
		numA := extractPageNumber(a.Title)
		numB := extractPageNumber(b.Title)
		return cmp.Compare(numA, numB)
	})

	return filteredPages, nil
}

// extractPageNumber извлекает первое число из начала заголовка страницы
// Например, "14 Grammar Sequence of Tenses" -> 14
// Если число не найдено, возвращает максимальное значение int для сортировки в конец
func extractPageNumber(title string) int {
	// Удаляем пробелы в начале
	title = strings.TrimSpace(title)
	if title == "" {
		return 0
	}

	// Ищем первое число в начале строки
	re := regexp.MustCompile(`^\d+`)
	match := re.FindString(title)
	if match == "" {
		// Если число не найдено, возвращаем максимальное значение для сортировки в конец
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

func (s *Service) UpdateReviewProgress(ctx context.Context, telegramID int64, pageID string, success bool) error {
	progress, err := s.repo.GetProgress(ctx, telegramID, pageID)
	if err != nil {
		return fmt.Errorf("get progress (telegram_id: %d, page_id: %s): %w", telegramID, pageID, err)
	}

	nextReview, newInterval := srs.CalculateNextReviewDate(progress.IntervalDays, success)

	newRepCount := progress.RepetitionCount
	if success {
		newRepCount++
	}

	history := models.ProgressHistory{
		Date:   time.Now(),
		Score:  0,
		Passed: success,
		Mode:   "standard",
		Notes:  "",
	}

	if err := s.repo.UpdateProgress(ctx, telegramID, pageID, newRepCount, time.Now(), nextReview, newInterval); err != nil {
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
