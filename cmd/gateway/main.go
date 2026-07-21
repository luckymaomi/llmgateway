package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/luckymaomi/llmgateway/internal/app"
	"github.com/luckymaomi/llmgateway/internal/buildinfo"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/security"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, buildinfo.JSON())
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--check-config" {
		if _, err := config.Load(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "configuration valid")
		return
	}
	if err := runPlatform(runGateway); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type gatewayRunner func(context.Context, io.Writer) error

func runGateway(ctx context.Context, output io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	logger := slog.New(security.NewRedactingHandler(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: cfg.LogLevel()})))
	slog.SetDefault(logger)

	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("application initialization failed", "error", err)
		return err
	}

	if err := application.Run(ctx); err != nil {
		logger.Error("application stopped with an error", "error", err)
		return err
	}
	return nil
}
