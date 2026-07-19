package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/migrations"
)

func main() {
	action := flag.String("action", "status", "migration action: status, up, or rebuild")
	confirmDataLoss := flag.Bool("confirm-development-data-loss", false, "confirm rebuilding the configured development or test database")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	database, err := sql.Open("pgx", cfg.Database.URL)
	if err != nil {
		fatal(fmt.Errorf("open database: %w", err))
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch *action {
	case "status":
		err = migrations.Status(ctx, database)
	case "up":
		err = migrations.Up(ctx, database)
	case "rebuild":
		if err = authorizeRebuild(cfg, *confirmDataLoss); err == nil {
			err = migrations.Reset(ctx, database)
		}
	default:
		err = fmt.Errorf("unsupported action %q", *action)
	}
	if err != nil {
		fatal(err)
	}
}

func authorizeRebuild(cfg config.Config, confirmed bool) error {
	if cfg.Profile == config.ProfileProduction {
		return fmt.Errorf("database rebuild is disabled in production")
	}
	if !confirmed {
		return fmt.Errorf("rebuild requires --confirm-development-data-loss")
	}
	parsed, err := url.Parse(cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if databaseName == "" || databaseName == "postgres" || strings.HasPrefix(databaseName, "template") {
		return fmt.Errorf("refusing to rebuild unsafe database name %q", databaseName)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
