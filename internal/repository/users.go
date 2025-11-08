package repository

import (
	"context"

	"github.com/yourusername/master-english-srs/internal/models"
)

func (r Postgres) CreateUser(ctx context.Context, user *models.User) error {
	query := r.psql.Insert("users").
		Columns("telegram_id", "username", "level", "use_manual_pages", "reminder_time", "created_at").
		Values(user.TelegramID, user.Username, user.Level, user.UseManualPages, user.ReminderTime, user.CreatedAt)

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	return err
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
		return nil, err
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
		return false, err
	}

	var count int
	err = r.db.QueryRowContext(ctx, sql, args...).Scan(&count)
	return count > 0, err
}

func (r Postgres) UpdateUserLevel(ctx context.Context, telegramID int64, level string) error {
	query := r.psql.Update("users").
		Set("level", level).
		Where("telegram_id = ?", telegramID)

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	return err
}

func (r Postgres) UpdateOneNoteAuth(ctx context.Context, telegramID int64, auth *models.OneNoteAuth) error {
	query := r.psql.Update("users").
		Set("onenote_access_token", auth.AccessToken).
		Set("onenote_refresh_token", auth.RefreshToken).
		Set("onenote_expires_at", auth.ExpiresAt).
		Where("telegram_id = ?", telegramID)

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	return err
}

func (r Postgres) UpdateOneNoteConfig(ctx context.Context, telegramID int64, config *models.OneNoteConfig) error {
	query := r.psql.Update("users").
		Set("onenote_notebook_id", config.NotebookID).
		Set("onenote_section_id", config.SectionID).
		Where("telegram_id = ?", telegramID)

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	return err
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
		return nil, err
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
			return nil, err
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

	return users, rows.Err()
}
