package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/pressly/goose/v3"
)

type Postgres struct {
	db   *sqlx.DB
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

func (r Postgres) Begin() (*sqlx.Tx, error) {
	tx, err := r.db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return tx, nil
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

func commitOrRollback(tx *sqlx.Tx, err *error) {
	if *err == nil {
		*err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
}
