package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
	"github.com/romanzh1/master-english-srs/internal/models"
)

type Postgres struct {
	db   *sqlx.DB
	tx   *sqlx.Tx
	psql squirrel.StatementBuilderType
}

func NewDB(dsn string, maxIdle, maxOpen int) (*Postgres, error) {
	db, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	db.SetMaxIdleConns(maxIdle)
	db.SetMaxOpenConns(maxOpen)
	db.SetConnMaxLifetime(time.Hour)
	db.SetConnMaxIdleTime(time.Minute * 10)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	psql := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

	return &Postgres{db: db, psql: psql}, nil
}

func (r Postgres) Close() error {
	return r.db.Close()
}

func (r Postgres) Reset(dir string) error {
	if err := goose.Reset(r.db.DB, dir); err != nil {
		return fmt.Errorf("reset migrations (dir: %s): %w", dir, err)
	}

	return nil
}

func (r Postgres) Up(dir string) error {
	if err := goose.Up(r.db.DB, dir); err != nil {
		return fmt.Errorf("run migrations (dir: %s): %w", dir, err)
	}

	return nil
}

func (r *Postgres) Begin() (*Postgres, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}

	return &Postgres{
		db:   r.db,
		tx:   tx,
		psql: r.psql,
	}, nil
}

func (r *Postgres) Commit() error {
	if r.tx == nil {
		return fmt.Errorf("no active transaction to commit")
	}
	return r.tx.Commit()
}

func (r *Postgres) Rollback() error {
	if r.tx == nil {
		return fmt.Errorf("no active transaction to rollback")
	}
	return r.tx.Rollback()
}

func (r *Postgres) RunInTx(ctx context.Context, fn func(models.Repository) error) error {
	txRepo, err := r.Begin()
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = txRepo.Rollback()
			panic(p)
		}
	}()

	if err = fn(txRepo); err != nil {
		_ = txRepo.Rollback()
		return err
	}

	return txRepo.Commit()
}

func (r *Postgres) executor() sqlx.ExtContext {
	if r.tx != nil {
		return r.tx
	}
	return r.db
}

func (r *Postgres) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return r.executor().ExecContext(ctx, query, args...)
}

func (r *Postgres) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return r.executor().QueryContext(ctx, query, args...)
}

func (r *Postgres) QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row {
	return r.executor().QueryRowxContext(ctx, query, args...)
}

func (r *Postgres) GetContext(ctx context.Context, dest any, query string, args ...any) error {
	return sqlx.GetContext(ctx, r.executor(), dest, query, args...)
}

func (r *Postgres) SelectContext(ctx context.Context, dest any, query string, args ...any) error {
	return sqlx.SelectContext(ctx, r.executor(), dest, query, args...)
}
