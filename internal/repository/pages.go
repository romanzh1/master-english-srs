package repository

import (
	"context"
	"fmt"

	"github.com/romanzh1/master-english-srs/internal/models"
)

func (r Postgres) CreatePageReference(ctx context.Context, page *models.PageReference) error {
	query := r.psql.Insert("page_references").
		Columns("page_id", "user_id", "title", "source", "created_at", "updated_at").
		Values(page.PageID, page.UserID, page.Title, page.Source, page.CreatedAt, page.UpdatedAt)

	sql, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build SQL query (page_id: %s, user_id: %d): %w", page.PageID, page.UserID, err)
	}

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("create page reference (page_id: %s, user_id: %d, title: %s): %w", page.PageID, page.UserID, page.Title, err)
	}
	return nil
}

func (r Postgres) GetPageReference(ctx context.Context, pageID string, userID int64) (*models.PageReference, error) {
	query := `
		SELECT page_id, user_id, title, source, created_at, updated_at
		FROM page_references
		WHERE page_id = $1 AND user_id = $2
	`

	var page models.PageReference
	err := r.GetContext(ctx, &page, query, pageID, userID)
	if err != nil {
		return nil, fmt.Errorf("get page reference (page_id: %s, user_id: %d): %w", pageID, userID, err)
	}

	return &page, nil
}

func (r Postgres) GetUserPagesInProgress(ctx context.Context, userID int64) ([]*models.PageReference, error) {
	query := `SELECT page_id, user_id, title, source, created_at, updated_at FROM page_references WHERE user_id = $1`

	var pages []*models.PageReference
	err := r.SelectContext(ctx, &pages, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query user pages (user_id: %d): %w", userID, err)
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

	_, err = r.ExecContext(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("delete user pages (user_id: %d): %w", userID, err)
	}
	return nil
}

func (r Postgres) UpsertPageReference(ctx context.Context, page *models.PageReference) error {
	query := `
		INSERT INTO page_references (page_id, user_id, title, source, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (page_id, user_id) 
		DO UPDATE SET 
			title = EXCLUDED.title,
			source = EXCLUDED.source,
			updated_at = EXCLUDED.updated_at
	`

	_, err := r.ExecContext(ctx, query, page.PageID, page.UserID, page.Title, page.Source, page.CreatedAt, page.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert page reference (page_id: %s, user_id: %d, title: %s): %w", page.PageID, page.UserID, page.Title, err)
	}
	return nil
}
