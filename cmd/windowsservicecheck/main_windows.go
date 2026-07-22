//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc/mgr"
)

func main() {
	serviceName := flag.String("name", "LLMGateway", "Windows service name")
	flag.Parse()
	if err := verify(*serviceName); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Windows service recovery policy is valid.")
}

func verify(serviceName string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("open Windows service %q: %w", serviceName, err)
	}
	defer service.Close()

	actions, err := service.RecoveryActions()
	if err != nil {
		return fmt.Errorf("read Windows service recovery actions: %w", err)
	}
	expected := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.NoAction, Delay: 0},
	}
	if len(actions) != len(expected) {
		return fmt.Errorf("Windows service recovery action count = %d, want %d", len(actions), len(expected))
	}
	for index := range expected {
		if actions[index] != expected[index] {
			return fmt.Errorf("Windows service recovery action %d = %+v, want %+v", index+1, actions[index], expected[index])
		}
	}

	resetPeriodSeconds, err := service.ResetPeriod()
	if err != nil {
		return fmt.Errorf("read Windows service recovery reset period: %w", err)
	}
	if resetPeriodSeconds != 86400 {
		return fmt.Errorf("Windows service recovery reset period = %d seconds, want 86400", resetPeriodSeconds)
	}

	nonCrashFailures, err := service.RecoveryActionsOnNonCrashFailures()
	if err != nil {
		return fmt.Errorf("read Windows non-crash recovery flag: %w", err)
	}
	if !nonCrashFailures {
		return fmt.Errorf("Windows service recovery is disabled for non-crash failures")
	}
	return nil
}
