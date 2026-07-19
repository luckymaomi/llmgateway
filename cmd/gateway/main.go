package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/luckymaomi/llmgateway/internal/app"
	"github.com/luckymaomi/llmgateway/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel()}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("application initialization failed", "error", err)
		os.Exit(1)
	}

	if err := application.Run(ctx); err != nil {
		logger.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}
