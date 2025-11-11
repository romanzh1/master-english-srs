package service

import (
	"context"
	"fmt"
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
	UpdateOneNoteConfig(ctx context.Context, telegramID int64, config *models.OneNoteConfig) error
	GetAllUsersWithReminders(ctx context.Context) ([]*models.User, error)

	CreatePageReference(ctx context.Context, page *models.PageReference) error
	GetPageReference(ctx context.Context, pageID string, userID int64) (*models.PageReference, error)
	GetUserPages(ctx context.Context, userID int64) ([]*models.PageReference, error)
	DeleteUserPages(ctx context.Context, userID int64) error
	GetMaxPageNumber(ctx context.Context, userID int64) (int, error)

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

	return nil
}

func (s *Service) GetOneNoteNotebooks(ctx context.Context, telegramID int64) ([]onenote.Notebook, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteAuth == nil {
		return nil, fmt.Errorf("onenote not connected (telegram_id: %d)", telegramID)
	}

	notebooks, err := s.oneNoteClient.GetNotebooks(user.OneNoteAuth.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("get notebooks (telegram_id: %d): %w", telegramID, err)
	}

	return notebooks, nil
}

func (s *Service) GetOneNoteSections(ctx context.Context, telegramID int64, notebookID string) ([]onenote.Section, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteAuth == nil {
		return nil, fmt.Errorf("onenote not connected (telegram_id: %d)", telegramID)
	}

	sections, err := s.oneNoteClient.GetSections(user.OneNoteAuth.AccessToken, notebookID)
	if err != nil {
		return nil, fmt.Errorf("get sections (telegram_id: %d, notebook_id: %s): %w", telegramID, notebookID, err)
	}

	return sections, nil
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

func (s *Service) SyncPages(ctx context.Context, telegramID int64) (int, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return 0, fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteAuth == nil || user.OneNoteConfig == nil {
		return 0, fmt.Errorf("onenote not configured (telegram_id: %d)", telegramID)
	}

	pages, err := s.oneNoteClient.GetPages(user.OneNoteAuth.AccessToken, user.OneNoteConfig.SectionID)
	if err != nil {
		return 0, fmt.Errorf("get pages (telegram_id: %d, section_id: %s): %w", telegramID, user.OneNoteConfig.SectionID, err)
	}

	if err := s.repo.DeleteUserPages(ctx, telegramID); err != nil {
		zap.S().Error("delete user pages", zap.Error(err), zap.Int64("telegram_id", telegramID))
	}

	for i, page := range pages {
		pageRef := &models.PageReference{
			PageID:     page.ID,
			UserID:     telegramID,
			Title:      page.Title,
			PageNumber: i + 1,
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

func (s *Service) GetDuePagesToday(ctx context.Context, telegramID int64) ([]*models.PageWithProgress, error) {
	return s.repo.GetDuePagesToday(ctx, telegramID)
}

func (s *Service) GetUserPages(ctx context.Context, telegramID int64) ([]*models.PageReference, error) {
	return s.repo.GetUserPages(ctx, telegramID)
}

func (s *Service) GetPageContent(ctx context.Context, telegramID int64, pageID string) (string, error) {
	user, err := s.repo.GetUser(ctx, telegramID)
	if err != nil {
		return "", fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.OneNoteAuth == nil {
		return "", fmt.Errorf("onenote not connected (telegram_id: %d)", telegramID)
	}

	content, err := s.oneNoteClient.GetPageContent(user.OneNoteAuth.AccessToken, pageID)
	if err != nil {
		return "", fmt.Errorf("get page content (telegram_id: %d, page_id: %s): %w", telegramID, pageID, err)
	}

	return content, nil
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
