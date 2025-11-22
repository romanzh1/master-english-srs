package models

import (
	"context"
	"time"

	"github.com/romanzh1/master-english-srs/pkg/onenote"
)

type Repository interface {
	CreateUser(ctx context.Context, user *User) error
	GetUser(ctx context.Context, telegramID int64) (*User, error)
	UserExists(ctx context.Context, telegramID int64) (bool, error)
	UpdateUserLevel(ctx context.Context, telegramID int64, level string) error
	UpdateOneNoteAuth(ctx context.Context, telegramID int64, auth *OneNoteAuth) error
	UpdateAuthCode(ctx context.Context, telegramID int64, authCode string) error
	UpdateOneNoteConfig(ctx context.Context, telegramID int64, config *OneNoteConfig) error
	UpdateMaxPagesPerDay(ctx context.Context, telegramID int64, maxPages uint) error
	GetAllUsersWithReminders(ctx context.Context) ([]*User, error)
	SetMaterialsPreparedAt(ctx context.Context, telegramID int64, preparedAt time.Time) error
	RunInTx(ctx context.Context, fn func(Repository) error) error

	CreatePageReference(ctx context.Context, page *PageReference) error
	GetPageReference(ctx context.Context, pageID string, userID int64) (*PageReference, error)
	GetUserPagesInProgress(ctx context.Context, userID int64) ([]*PageReference, error)
	DeleteUserPages(ctx context.Context, userID int64) error
	UpsertPageReference(ctx context.Context, page *PageReference) error

	CreateProgress(ctx context.Context, progress *UserProgress) error
	GetProgress(ctx context.Context, userID int64, pageID string) (*UserProgress, error)
	UpdateProgress(ctx context.Context, userID int64, pageID string, level string, repetitionCount int, lastReviewDate, nextReviewDate time.Time, intervalDays int, reviewedToday bool, passed bool) error
	AddProgressHistory(ctx context.Context, userID int64, pageID string, history ProgressHistory) error
	GetDuePagesToday(ctx context.Context, userID int64) ([]*UserProgress, error)
	GetAllProgressPageIDs(ctx context.Context, userID int64) ([]string, error)
	GetPageIDsNotInProgress(ctx context.Context, userID int64, pageIDs []string) ([]string, error)
	ProgressExists(ctx context.Context, userID int64, pageID string) (bool, error)
	ResetReviewedTodayFlag(ctx context.Context, userID int64) error
	GetLastReviewScore(ctx context.Context, userID int64, pageID string) (int, error)
	DeleteProgress(ctx context.Context, userID int64, pageID string) error

	UpdateUserActivity(ctx context.Context, userID int64, activityDate time.Time) error
	SetUserPaused(ctx context.Context, userID int64, paused bool) error
	GetUsersWithoutActivityForWeek(ctx context.Context) ([]*User, error)
	GetUsersWithoutActivityForMonth(ctx context.Context) ([]*User, error)

	CountPagesInProgress(ctx context.Context, userID int64) (int, error)
	GetPagesDueInNextMonth(ctx context.Context, userID int64) ([]*UserProgress, error)
	ResetIntervalForPagesDueInMonth(ctx context.Context, userID int64) error
}

type Service interface {
	RegisterUser(ctx context.Context, telegramID int64, username, level string) error
	GetUser(ctx context.Context, telegramID int64) (*User, error)
	UserExists(ctx context.Context, telegramID int64) (bool, error)
	UpdateUserLevel(ctx context.Context, telegramID int64, level string) error
	GetAllUsersForReminders(ctx context.Context) ([]*User, error)

	GetAuthURL(telegramID int64) string
	ExchangeAuthCode(ctx context.Context, telegramID int64, code string) error

	GetOneNoteNotebooks(ctx context.Context, telegramID int64) ([]onenote.Notebook, error)
	GetOneNoteSections(ctx context.Context, telegramID int64, notebookID string) ([]onenote.Section, error)
	SaveOneNoteConfig(ctx context.Context, telegramID int64, notebookID, sectionID string) error

	GetDuePagesToday(ctx context.Context, telegramID int64) ([]*PageWithProgress, error)
	GetUserAllPagesInProgress(ctx context.Context, telegramID int64) ([]*PageReference, error)
	GetPageContent(ctx context.Context, telegramID int64, pageID string) (string, error)
	UpdateReviewProgress(ctx context.Context, telegramID int64, pageID string, grade int) error
	UpdateMaxPagesPerDay(ctx context.Context, telegramID int64, maxPages uint) error
	GetProgress(ctx context.Context, telegramID int64, pageID string) (*UserProgress, error)
	GetLastReviewScore(ctx context.Context, telegramID int64, pageID string) (int, error)
	SkipPage(ctx context.Context, userID int64, pageID string) error
	RunDailyCron(ctx context.Context) error
	PrepareMaterials(ctx context.Context, telegramID int64) error
}
