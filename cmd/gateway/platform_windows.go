//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const windowsServiceName = "LLMGateway"

func runPlatform(run gatewayRunner) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("detect Windows service host: %w", err)
	}
	if !isService {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return run(ctx, os.Stdout)
	}
	return svc.Run(windowsServiceName, &windowsService{run: run})
}

type windowsService struct {
	run gatewayRunner
}

func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	eventWriter, err := newEventLogWriter()
	if err != nil {
		return false, 1
	}
	defer eventWriter.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.run(ctx, eventWriter) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-result:
			if err != nil {
				_, _ = eventWriter.Write([]byte(fmt.Sprintf(`{"level":"ERROR","msg":"Windows service stopped","error":%q}`, err.Error())))
				return false, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 35_000}
				cancel()
				if err := <-result; err != nil {
					return false, 1
				}
				return false, 0
			}
		}
	}
}

type eventLogWriter struct {
	log *eventlog.Log
}

func newEventLogWriter() (*eventLogWriter, error) {
	log, err := eventlog.Open(windowsServiceName)
	if err != nil {
		return nil, fmt.Errorf("open Windows Event Log source: %w", err)
	}
	return &eventLogWriter{log: log}, nil
}

func (w *eventLogWriter) Write(message []byte) (int, error) {
	text := strings.TrimSpace(string(message))
	if len(text) > 30_000 {
		text = text[:30_000]
	}
	if err := w.log.Info(1, text); err != nil {
		return 0, err
	}
	return len(message), nil
}

func (w *eventLogWriter) Close() error {
	return w.log.Close()
}

var _ io.WriteCloser = (*eventLogWriter)(nil)
