package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/yourusername/master-english-srs/internal/models"
)

func (r Postgres) CreateProgress(ctx context.Context, progress *models.UserProgress) error {
	query := r.psql.Insert("user_progress").
		Columns("user_id", "page_id", "repetition_count", "last_review_date", "next_review_date", "interval_days", "success_rate").
		Values(progress.UserID, progress.PageID, progress.RepetitionCount, progress.LastReviewDate, progress.NextReviewDate, progress.IntervalDays, progress.SuccessRate)

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	return err
}

func (r Postgres) GetProgress(ctx context.Context, userID int64, pageID string) (*models.UserProgress, error) {
	query := `
		SELECT user_id, page_id, repetition_count, last_review_date, next_review_date, interval_days, success_rate
		FROM user_progress
		WHERE user_id = $1 AND page_id = $2
	`

	var progress models.UserProgress
	var lastReview sql.NullTime

	err := r.db.QueryRowContext(ctx, query, userID, pageID).Scan(
		&progress.UserID,
		&progress.PageID,
		&progress.RepetitionCount,
		&lastReview,
		&progress.NextReviewDate,
		&progress.IntervalDays,
		&progress.SuccessRate,
	)

	if err != nil {
		return nil, err
	}

	if lastReview.Valid {
		progress.LastReviewDate = lastReview.Time
	}

	return &progress, nil
}

func (r Postgres) UpdateProgress(ctx context.Context, userID int64, pageID string, repetitionCount int, lastReviewDate, nextReviewDate time.Time, intervalDays int) error {
	query := r.psql.Update("user_progress").
		Set("repetition_count", repetitionCount).
		Set("last_review_date", lastReviewDate).
		Set("next_review_date", nextReviewDate).
		Set("interval_days", intervalDays).
		Where("user_id = ? AND page_id = ?", userID, pageID)

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	return err
}

func (r Postgres) AddProgressHistory(ctx context.Context, userID int64, pageID string, history models.ProgressHistory) error {
	query := r.psql.Insert("progress_history").
		Columns("user_id", "page_id", "date", "score", "passed", "mode", "notes").
		Values(userID, pageID, history.Date, history.Score, history.Passed, history.Mode, history.Notes)

	sql, args, err := query.ToSql()
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	return err
}

func (r Postgres) GetDuePagesToday(ctx context.Context, userID int64) ([]*models.PageWithProgress, error) {
	now := time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)

	query := `
		SELECT 
			pr.page_id, pr.user_id, pr.title, pr.page_number, pr.category, pr.level, pr.source, pr.created_at, pr.last_synced,
			up.repetition_count, up.last_review_date, up.next_review_date, up.interval_days, up.success_rate
		FROM page_references pr
		JOIN user_progress up ON pr.page_id = up.page_id AND pr.user_id = up.user_id
		WHERE pr.user_id = $1 AND up.next_review_date <= $2
		ORDER BY up.next_review_date ASC
	`

	rows, err := r.db.QueryContext(ctx, query, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*models.PageWithProgress
	for rows.Next() {
		var pwp models.PageWithProgress
		var lastReview sql.NullTime

		err := rows.Scan(
			&pwp.Page.PageID,
			&pwp.Page.UserID,
			&pwp.Page.Title,
			&pwp.Page.PageNumber,
			&pwp.Page.Category,
			&pwp.Page.Level,
			&pwp.Page.Source,
			&pwp.Page.CreatedAt,
			&pwp.Page.LastSynced,
			&pwp.Progress.RepetitionCount,
			&lastReview,
			&pwp.Progress.NextReviewDate,
			&pwp.Progress.IntervalDays,
			&pwp.Progress.SuccessRate,
		)
		if err != nil {
			return nil, err
		}

		pwp.Progress = &models.UserProgress{
			UserID:          pwp.Page.UserID,
			PageID:          pwp.Page.PageID,
			RepetitionCount: pwp.Progress.RepetitionCount,
			NextReviewDate:  pwp.Progress.NextReviewDate,
			IntervalDays:    pwp.Progress.IntervalDays,
			SuccessRate:     pwp.Progress.SuccessRate,
		}

		if lastReview.Valid {
			pwp.Progress.LastReviewDate = lastReview.Time
		}

		result = append(result, &pwp)
	}

	return result, rows.Err()
}

func (r Postgres) ProgressExists(ctx context.Context, userID int64, pageID string) (bool, error) {
	query := r.psql.Select("COUNT(*)").From("user_progress").Where("user_id = ? AND page_id = ?", userID, pageID)

	sql, args, err := query.ToSql()
	if err != nil {
		return false, err
	}

	var count int
	err = r.db.QueryRowContext(ctx, sql, args...).Scan(&count)
	return count > 0, err
}
