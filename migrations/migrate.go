package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

func Up(ctx context.Context, database *sql.DB) error {
	if err := prepare(); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func Reset(ctx context.Context, database *sql.DB) error {
	if err := prepare(); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.ResetContext(ctx, database, "."); err != nil {
		return fmt.Errorf("reset migrations: %w", err)
	}
	if err := goose.UpContext(ctx, database, "."); err != nil {
		return fmt.Errorf("rebuild migrations: %w", err)
	}
	return nil
}

func Status(ctx context.Context, database *sql.DB) error {
	if err := prepare(); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.StatusContext(ctx, database, "."); err != nil {
		return fmt.Errorf("migration status: %w", err)
	}
	return nil
}

func prepare() error {
	goose.SetBaseFS(files)
	return goose.SetDialect("postgres")
}
