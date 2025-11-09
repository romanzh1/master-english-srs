package repository

import (
	"context"
	"fmt"

	"github.com/yourusername/master-english-srs/internal/models"
)

func (r Postgres) CreatePageReference(ctx context.Context, page *models.PageReference) error {
	query := r.psql.Insert("page_references").
		Columns("page_id", "user_id", "title", "page_number", "category", "level", "source", "created_at", "last_synced").
		Values(page.PageID, page.UserID, page.Title, page.PageNumber, page.Category, page.Level, page.Source, page.CreatedAt, page.LastSynced)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (page_id: %s, user_id: %d): %w", page.PageID, page.UserID, err)
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("create page reference (page_id: %s, user_id: %d, title: %s): %w", page.PageID, page.UserID, page.Title, err)
	}
	return nil
}

func (r Postgres) GetPageReference(ctx context.Context, pageID string, userID int64) (*models.PageReference, error) {
	query := `
		SELECT page_id, user_id, title, page_number, category, level, source, created_at, last_synced
		FROM page_references
		WHERE page_id = $1 AND user_id = $2
	`

	var page models.PageReference
	err := r.db.QueryRowContext(ctx, query, pageID, userID).Scan(
		&page.PageID,
		&page.UserID,
		&page.Title,
		&page.PageNumber,
		&page.Category,
		&page.Level,
		&page.Source,
		&page.CreatedAt,
		&page.LastSynced,
	)

	if err != nil {
		return nil, fmt.Errorf("get page reference (page_id: %s, user_id: %d): %w", pageID, userID, err)
	}

	return &page, nil
}

func (r Postgres) GetUserPages(ctx context.Context, userID int64) ([]*models.PageReference, error) {
	query := `
		SELECT page_id, user_id, title, page_number, category, level, source, created_at, last_synced
		FROM page_references
		WHERE user_id = $1
		ORDER BY page_number ASC
	`

	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query user pages (user_id: %d): %w", userID, err)
	}
	defer rows.Close()

	var pages []*models.PageReference
	for rows.Next() {
		var page models.PageReference
		err := rows.Scan(
			&page.PageID,
			&page.UserID,
			&page.Title,
			&page.PageNumber,
			&page.Category,
			&page.Level,
			&page.Source,
			&page.CreatedAt,
			&page.LastSynced,
		)
		if err != nil {
			return nil, fmt.Errorf("scan page row (user_id: %d): %w", userID, err)
		}
		pages = append(pages, &page)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate page rows (user_id: %d): %w", userID, err)
	}

	return pages, nil
}

func (r Postgres) DeleteUserPages(ctx context.Context, userID int64) error {
	query := r.psql.Delete("page_references").
		Where("user_id = ?", userID)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (user_id: %d): %w", userID, err)
	}

	_, err = r.db.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("delete user pages (user_id: %d): %w", userID, err)
	}
	return nil
}

func (r Postgres) GetMaxPageNumber(ctx context.Context, userID int64) (int, error) {
	query := r.psql.Select("COALESCE(MAX(page_number), 0)").
		From("page_references").
		Where("user_id = ?", userID)

	sql, args, err := query.ToSql()
	if err != nil {
		return 0, fmt.Errorf("build SQL query (user_id: %d): %w", userID, err)
	}

	var maxNum int
	err = r.db.QueryRowContext(ctx, sql, args...).Scan(&maxNum)
	if err != nil {
		return 0, fmt.Errorf("get max page number (user_id: %d): %w", userID, err)
	}
	return maxNum, nil
}
