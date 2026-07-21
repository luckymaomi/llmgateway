//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func runPlatform(run gatewayRunner) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, os.Stdout)
}
