package repository

import (
	"context"
	"fmt"

	"github.com/yourusername/master-english-srs/internal/models"
)

func (r Postgres) CreateUser(ctx context.Context, user *models.User) error {
	query := r.psql.Insert("users").
		Columns("telegram_id", "username", "level", "use_manual_pages", "reminder_time", "created_at").
		Values(user.TelegramID, user.Username, user.Level, user.UseManualPages, user.ReminderTime, user.CreatedAt)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (telegram_id: %d): %w", user.TelegramID, err)
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("create user (telegram_id: %d, username: %s): %w", user.TelegramID, user.Username, err)
	}
	return nil
}

func (r Postgres) GetUser(ctx context.Context, telegramID int64) (*models.User, error) {
	query := `
		SELECT telegram_id, username, level, onenote_access_token, onenote_refresh_token, 
		       onenote_expires_at, onenote_notebook_id, onenote_section_id, 
		       use_manual_pages, reminder_time, created_at
		FROM users WHERE telegram_id = $1
	`

	var user models.User
	err := r.db.QueryRowContext(ctx, query, telegramID).Scan(
		&user.TelegramID,
		&user.Username,
		&user.Level,
		&user.AccessToken,
		&user.RefreshToken,
		&user.ExpiresAt,
		&user.NotebookID,
		&user.SectionID,
		&user.UseManualPages,
		&user.ReminderTime,
		&user.CreatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("get user (telegram_id: %d): %w", telegramID, err)
	}

	if user.AccessToken != nil && user.RefreshToken != nil && user.ExpiresAt != nil {
		user.OneNoteAuth = &models.OneNoteAuth{
			AccessToken:  *user.AccessToken,
			RefreshToken: *user.RefreshToken,
			ExpiresAt:    *user.ExpiresAt,
		}
	}

	if user.NotebookID != nil && user.SectionID != nil {
		user.OneNoteConfig = &models.OneNoteConfig{
			NotebookID: *user.NotebookID,
			SectionID:  *user.SectionID,
		}
	}

	return &user, nil
}

func (r Postgres) UserExists(ctx context.Context, telegramID int64) (bool, error) {
	query := r.psql.Select("COUNT(*)").From("users").Where("telegram_id = ?", telegramID)

	sql, args, err := query.ToSql()
	if err != nil {
		return false, fmt.Errorf("build SQL query (telegram_id: %d): %w", telegramID, err)
	}

	var count int
	err = r.db.QueryRowContext(ctx, sql, args...).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check user exists (telegram_id: %d): %w", telegramID, err)
	}
	return count > 0, nil
}

func (r Postgres) UpdateUserLevel(ctx context.Context, telegramID int64, level string) error {
	query := r.psql.Update("users").
		Set("level", level).
		Where("telegram_id = ?", telegramID)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (telegram_id: %d, level: %s): %w", telegramID, level, err)
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("update user level (telegram_id: %d, level: %s): %w", telegramID, level, err)
	}
	return nil
}

func (r Postgres) UpdateOneNoteAuth(ctx context.Context, telegramID int64, auth *models.OneNoteAuth) error {
	query := r.psql.Update("users").
		Set("onenote_access_token", auth.AccessToken).
		Set("onenote_refresh_token", auth.RefreshToken).
		Set("onenote_expires_at", auth.ExpiresAt).
		Where("telegram_id = ?", telegramID)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (telegram_id: %d): %w", telegramID, err)
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("update OneNote auth (telegram_id: %d): %w", telegramID, err)
	}
	return nil
}

func (r Postgres) UpdateOneNoteConfig(ctx context.Context, telegramID int64, config *models.OneNoteConfig) error {
	query := r.psql.Update("users").
		Set("onenote_notebook_id", config.NotebookID).
		Set("onenote_section_id", config.SectionID).
		Where("telegram_id = ?", telegramID)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (telegram_id: %d, notebook_id: %s): %w", telegramID, config.NotebookID, err)
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("update OneNote config (telegram_id: %d, notebook_id: %s): %w", telegramID, config.NotebookID, err)
	}
	return nil
}

func (r Postgres) GetAllUsersWithReminders(ctx context.Context) ([]*models.User, error) {
	query := `
		SELECT telegram_id, username, level, onenote_access_token, onenote_refresh_token, 
		       onenote_expires_at, onenote_notebook_id, onenote_section_id, 
		       use_manual_pages, reminder_time, created_at
		FROM users
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	var users []*models.User
	for rows.Next() {
		var user models.User
		err := rows.Scan(
			&user.TelegramID,
			&user.Username,
			&user.Level,
			&user.AccessToken,
			&user.RefreshToken,
			&user.ExpiresAt,
			&user.NotebookID,
			&user.SectionID,
			&user.UseManualPages,
			&user.ReminderTime,
			&user.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan user row: %w", err)
		}

		if user.AccessToken != nil && user.RefreshToken != nil && user.ExpiresAt != nil {
			user.OneNoteAuth = &models.OneNoteAuth{
				AccessToken:  *user.AccessToken,
				RefreshToken: *user.RefreshToken,
				ExpiresAt:    *user.ExpiresAt,
			}
		}

		if user.NotebookID != nil && user.SectionID != nil {
			user.OneNoteConfig = &models.OneNoteConfig{
				NotebookID: *user.NotebookID,
				SectionID:  *user.SectionID,
			}
		}

		users = append(users, &user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user rows: %w", err)
	}

	return users, nil
}
