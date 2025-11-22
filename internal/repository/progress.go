package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/romanzh1/master-english-srs/internal/models"
	"github.com/romanzh1/master-english-srs/pkg/utils"
)

func (r Postgres) CreateProgress(ctx context.Context, progress *models.UserProgress) error {
	query := r.psql.Insert("user_progress").
		Columns("user_id", "page_id", "level", "repetition_count", "last_review_date", "next_review_date", "interval_days", "success_rate", "reviewed_today", "passed").
		Values(progress.UserID, progress.PageID, progress.Level, progress.RepetitionCount, progress.LastReviewDate, progress.NextReviewDate, progress.IntervalDays, progress.SuccessRate, progress.ReviewedToday, progress.Passed)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (user_id: %d, page_id: %s): %w", progress.UserID, progress.PageID, err)
	}

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("create progress (user_id: %d, page_id: %s): %w", progress.UserID, progress.PageID, err)
	}
	return nil
}

func (r Postgres) GetProgress(ctx context.Context, userID int64, pageID string) (*models.UserProgress, error) {
	query := `
		SELECT user_id, page_id, level, repetition_count, last_review_date, next_review_date, interval_days, success_rate, reviewed_today, passed
		FROM user_progress
		WHERE user_id = $1 AND page_id = $2
	`

	var progress models.UserProgress
	err := r.GetContext(ctx, &progress, query, userID, pageID)
	if err != nil {
		return nil, fmt.Errorf("get progress (user_id: %d, page_id: %s): %w", userID, pageID, err)
	}

	return &progress, nil
}

func (r Postgres) UpdateProgress(ctx context.Context, userID int64, pageID string, level string, repetitionCount int, lastReviewDate, nextReviewDate time.Time, intervalDays int, reviewedToday bool, passed bool) error {
	query := r.psql.Update("user_progress").
		Set("level", level).
		Set("repetition_count", repetitionCount).
		Set("last_review_date", lastReviewDate).
		Set("next_review_date", nextReviewDate).
		Set("interval_days", intervalDays).
		Set("reviewed_today", reviewedToday).
		Set("passed", passed).
		Where("user_id = ? AND page_id = ?", userID, pageID)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (user_id: %d, page_id: %s): %w", userID, pageID, err)
	}

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("update progress (user_id: %d, page_id: %s, repetition_count: %d): %w", userID, pageID, repetitionCount, err)
	}
	return nil
}

func (r Postgres) AddProgressHistory(ctx context.Context, userID int64, pageID string, history models.ProgressHistory) error {
	query := r.psql.Insert("progress_history").
		Columns("user_id", "page_id", "date", "score", "mode", "notes").
		Values(userID, pageID, history.Date, history.Score, history.Mode, history.Notes)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (user_id: %d, page_id: %s): %w", userID, pageID, err)
	}

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("add progress history (user_id: %d, page_id: %s, date: %s): %w", userID, pageID, history.Date.Format(time.RFC3339), err)
	}
	return nil
}

func (r Postgres) GetDuePagesToday(ctx context.Context, userID int64) ([]*models.UserProgress, error) {
	now := utils.TruncateToMinutes(time.Now())
	endOfDay := utils.StartOfDay(now).AddDate(0, 0, 1)

	query := `
		SELECT user_id, page_id, level, repetition_count, last_review_date, next_review_date, interval_days, success_rate, reviewed_today, passed
		FROM user_progress
		WHERE user_id = $1 AND next_review_date <= $2 AND reviewed_today = FALSE
		ORDER BY next_review_date ASC
	`

	var progressList []*models.UserProgress
	err := r.SelectContext(ctx, &progressList, query, userID, endOfDay)
	if err != nil {
		return nil, fmt.Errorf("query due pages (user_id: %d, cutoff_time: %s): %w", userID, endOfDay.Format(time.RFC3339), err)
	}

	return progressList, nil
}

func (r Postgres) ProgressExists(ctx context.Context, userID int64, pageID string) (bool, error) {
	query := r.psql.Select("COUNT(*)").From("user_progress").Where("user_id = ? AND page_id = ?", userID, pageID)

	sql, args, err := query.ToSql()
	if err != nil {
		return false, fmt.Errorf("build SQL query (user_id: %d, page_id: %s): %w", userID, pageID, err)
	}

	var count int
	err = r.QueryRowxContext(ctx, sql, args...).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check progress exists (user_id: %d, page_id: %s): %w", userID, pageID, err)
	}
	return count > 0, nil
}

func (r Postgres) GetAllProgressPageIDs(ctx context.Context, userID int64) ([]string, error) {
	query := `SELECT page_id FROM user_progress WHERE user_id = $1`

	var pageIDs []string
	err := r.SelectContext(ctx, &pageIDs, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query all progress page IDs (user_id: %d): %w", userID, err)
	}

	return pageIDs, nil
}

func (r Postgres) GetPageIDsNotInProgress(ctx context.Context, userID int64, pageIDs []string) ([]string, error) {
	if len(pageIDs) == 0 {
		return pageIDs, nil
	}

	query := r.psql.Select("page_id").
		From("user_progress").
		Where("user_id = ?", userID).
		Where(squirrel.Eq{"page_id": pageIDs})

	sql, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build SQL query (user_id: %d): %w", userID, err)
	}

	var existingPageIDs []string
	err = r.SelectContext(ctx, &existingPageIDs, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query existing page IDs (user_id: %d): %w", userID, err)
	}

	existingMap := make(map[string]bool, len(existingPageIDs))
	for _, id := range existingPageIDs {
		existingMap[id] = true
	}

	var notInProgress []string
	for _, id := range pageIDs {
		if !existingMap[id] {
			notInProgress = append(notInProgress, id)
		}
	}

	return notInProgress, nil
}

func (r Postgres) ResetReviewedTodayFlag(ctx context.Context, userID int64) error {
	query := r.psql.Update("user_progress").
		Set("reviewed_today", false).
		Where("user_id = ?", userID)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (user_id: %d): %w", userID, err)
	}

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("reset reviewed today flag (user_id: %d): %w", userID, err)
	}

	return nil
}

func (r Postgres) GetLastReviewScore(ctx context.Context, userID int64, pageID string) (int, error) {
	query := `
		SELECT score
		FROM progress_history
		WHERE user_id = $1 AND page_id = $2
		ORDER BY date DESC
		LIMIT 1
	`

	var score int
	err := r.GetContext(ctx, &score, query, userID, pageID)
	if err != nil {
		// Если записи нет, возвращаем 0
		return 0, nil
	}

	return score, nil
}

func (r Postgres) DeleteProgress(ctx context.Context, userID int64, pageID string) error {
	query := r.psql.Delete("user_progress").
		Where("user_id = ? AND page_id = ?", userID, pageID)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (user_id: %d, page_id: %s): %w", userID, pageID, err)
	}

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("delete progress (user_id: %d, page_id: %s): %w", userID, pageID, err)
	}

	return nil
}

func (r Postgres) CountPagesInProgress(ctx context.Context, userID int64) (int, error) {
	query := r.psql.Select("COUNT(*)").
		From("user_progress").
		Where("user_id = ?", userID)

	sql, args, err := query.ToSql()
	if err != nil {
		return 0, fmt.Errorf("build SQL query (user_id: %d): %w", userID, err)
	}

	var count int
	err = r.QueryRowxContext(ctx, sql, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pages in progress (user_id: %d): %w", userID, err)
	}

	return count, nil
}

func (r Postgres) GetPagesDueInNextMonth(ctx context.Context, userID int64) ([]*models.UserProgress, error) {
	now := utils.TruncateToMinutes(time.Now())
	today := utils.StartOfDay(now)
	monthFromNow := today.AddDate(0, 0, 30)

	query := `
		SELECT user_id, page_id, level, repetition_count, last_review_date, next_review_date, interval_days, success_rate, reviewed_today, passed
		FROM user_progress
		WHERE user_id = $1 AND next_review_date <= $2 AND passed = FALSE
		ORDER BY next_review_date ASC
	`

	var progressList []*models.UserProgress
	err := r.SelectContext(ctx, &progressList, query, userID, monthFromNow)
	if err != nil {
		return nil, fmt.Errorf("get pages due in next month (user_id: %d): %w", userID, err)
	}

	return progressList, nil
}

func (r Postgres) ResetIntervalForPagesDueInMonth(ctx context.Context, userID int64) error {
	now := utils.TruncateToMinutes(time.Now())
	today := utils.StartOfDay(now)
	monthFromNow := today.AddDate(0, 0, 30)
	tomorrow := today.AddDate(0, 0, 1)

	query := r.psql.Update("user_progress").
		Set("interval_days", 1).
		Set("next_review_date", tomorrow).
		Where("user_id = ?", userID).
		Where("next_review_date <= ?", monthFromNow).
		Where("passed = FALSE")

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (user_id: %d): %w", userID, err)
	}

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("reset interval for pages due in month (user_id: %d): %w", userID, err)
	}

	return nil
}
