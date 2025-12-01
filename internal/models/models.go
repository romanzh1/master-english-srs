package models

import "time"

type User struct {
	TelegramID     int64          `db:"telegram_id"`
	Username       string         `db:"username"`
	Level          string         `db:"level"`
	OneNoteAuth    *OneNoteAuth   `db:"-"`
	OneNoteConfig  *OneNoteConfig `db:"-"`
	UseManualPages bool           `db:"use_manual_pages"`
	ReminderTime   string         `db:"reminder_time"`
	CreatedAt      time.Time      `db:"created_at"`

	AccessToken         *string    `db:"onenote_access_token"`
	RefreshToken        *string    `db:"onenote_refresh_token"`
	ExpiresAt           *time.Time `db:"onenote_expires_at"`
	AuthCode            *string    `db:"onenote_auth_code"`
	NotebookID          *string    `db:"onenote_notebook_id"`
	SectionID           *string    `db:"onenote_section_id"`
	MaxPagesPerDay         *uint      `db:"max_pages_per_day"`
	IsPaused               *bool      `db:"is_paused"`
	LastActivityDate       *time.Time `db:"last_activity_date"`
	Timezone               *string    `db:"timezone"`
	LastCronProcessedAt    *time.Time `db:"last_cron_processed_at"`
}

type OneNoteAuth struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

type OneNoteConfig struct {
	NotebookID string
	SectionID  string
}

type PageReference struct {
	PageID    string     `db:"page_id"`
	UserID    int64      `db:"user_id"`
	Title     string     `db:"title"`
	Source    string     `db:"source"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt *time.Time `db:"updated_at"`
}

type UserProgress struct {
	UserID          int64     `db:"user_id"`
	PageID          string    `db:"page_id"`
	Level           string    `db:"level"`
	RepetitionCount int       `db:"repetition_count"`
	LastReviewDate  time.Time `db:"last_review_date"`
	NextReviewDate  time.Time `db:"next_review_date"`
	IntervalDays    int       `db:"interval_days"`
	SuccessRate     int       `db:"success_rate"`
	ReviewedToday   bool      `db:"reviewed_today"`
	Passed          bool      `db:"passed"`
}

type ProgressHistory struct {
	Date  time.Time `db:"date"`
	Score int       `db:"score"`
	Mode  string    `db:"mode"`
	Notes string    `db:"notes"`
}

type PageWithProgress struct {
	Page     PageReference
	Progress *UserProgress
}
