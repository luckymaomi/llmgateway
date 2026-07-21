package main

import (
	"sort"
	"time"
)

type phaseReport struct {
	Name                     string              `json:"name"`
	Requests                 int                 `json:"requests"`
	Completed                int                 `json:"completed"`
	ControlledRejections     int                 `json:"controlledRejections"`
	UnexpectedFailures       int                 `json:"unexpectedFailures"`
	UsersWithTerminal        int                 `json:"usersWithTerminal"`
	Statuses                 map[int]int         `json:"statuses"`
	Kinds                    map[requestKind]int `json:"kinds"`
	P50Milliseconds          float64             `json:"p50Milliseconds"`
	P95Milliseconds          float64             `json:"p95Milliseconds"`
	P99Milliseconds          float64             `json:"p99Milliseconds"`
	FirstByteP95Milliseconds float64             `json:"firstByteP95Milliseconds"`
}

type capacityReport struct {
	RunID           string           `json:"runId"`
	StartedAt       time.Time        `json:"startedAt"`
	DurationSeconds int64            `json:"durationSeconds"`
	Users           int              `json:"users"`
	ActiveUsers     int              `json:"activeUsers"`
	Phases          []phaseReport    `json:"phases"`
	Resources       resourceReport   `json:"resources"`
	Database        databaseSummary  `json:"database"`
	Provider        map[string]int64 `json:"provider"`
	Faults          []faultReport    `json:"faults"`
	Failures        []string         `json:"failures,omitempty"`
}

func summarizeResults(results []requestResult) []phaseReport {
	byPhase := map[string][]requestResult{}
	order := []string{"warmup", "steady", "extended_stream", "burst", "hotspot"}
	for _, result := range results {
		byPhase[result.Phase] = append(byPhase[result.Phase], result)
	}
	reports := make([]phaseReport, 0, len(order))
	for _, phase := range order {
		items := byPhase[phase]
		if len(items) == 0 {
			continue
		}
		report := phaseReport{Name: phase, Requests: len(items), Statuses: map[int]int{}, Kinds: map[requestKind]int{}}
		latencies := make([]time.Duration, 0, len(items))
		firstBytes := make([]time.Duration, 0, len(items))
		terminalUsers := map[string]struct{}{}
		for _, item := range items {
			report.Statuses[item.Status]++
			report.Kinds[item.Kind]++
			controlled := (phase == "burst" || phase == "hotspot") && item.Status == 429 && item.RetryAfter
			success := item.Status >= 200 && item.Status < 300 && item.Completed && item.Failure == ""
			if success {
				report.Completed++
				latencies = append(latencies, item.Latency)
				firstBytes = append(firstBytes, item.FirstByte)
				terminalUsers[item.UserID.String()] = struct{}{}
			} else if controlled {
				report.ControlledRejections++
				terminalUsers[item.UserID.String()] = struct{}{}
			} else {
				report.UnexpectedFailures++
			}
		}
		report.UsersWithTerminal = len(terminalUsers)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		sort.Slice(firstBytes, func(i, j int) bool { return firstBytes[i] < firstBytes[j] })
		report.P50Milliseconds = durationPercentile(latencies, 0.50).Seconds() * 1000
		report.P95Milliseconds = durationPercentile(latencies, 0.95).Seconds() * 1000
		report.P99Milliseconds = durationPercentile(latencies, 0.99).Seconds() * 1000
		report.FirstByteP95Milliseconds = durationPercentile(firstBytes, 0.95).Seconds() * 1000
		reports = append(reports, report)
	}
	return reports
}

func validateReport(report capacityReport, postgresConnectionLimit int64) []string {
	var failures []string
	for _, phase := range report.Phases {
		if phase.UnexpectedFailures != 0 {
			failures = append(failures, phase.Name+": unexpected failures")
		}
		if phase.Name == "steady" && phase.UsersWithTerminal != report.ActiveUsers {
			failures = append(failures, "steady: one or more active users starved")
		}
		if phase.Name == "extended_stream" && phase.UsersWithTerminal != report.ActiveUsers {
			failures = append(failures, "extended_stream: one or more active users starved")
		}
		if phase.Name == "burst" && phase.UsersWithTerminal != report.Users {
			failures = append(failures, "burst: one or more users lacked a terminal result")
		}
		if phase.Name == "extended_stream" {
			if phase.P95Milliseconds > 40000 || phase.P99Milliseconds > 45000 {
				failures = append(failures, phase.Name+": completion latency exceeded the extended-stream bound")
			}
		} else if phase.Name == "hotspot" {
			if phase.P95Milliseconds > 10000 {
				failures = append(failures, phase.Name+": long-stream p95 exceeded ten seconds")
			}
		} else if phase.P95Milliseconds > 2000 || phase.P99Milliseconds > 5000 {
			failures = append(failures, phase.Name+": p95/p99 exceeded the capacity bound")
		}
		if phase.FirstByteP95Milliseconds > 2000 {
			failures = append(failures, phase.Name+": first-byte p95 exceeded two seconds")
		}
	}
	if report.Resources.PeakResidentBytes > 768<<20 {
		failures = append(failures, "gateway resident memory exceeded 768 MiB")
	}
	if report.Resources.FinalGoroutines > report.Resources.BaselineGoroutines+80 {
		failures = append(failures, "gateway goroutines did not return near baseline")
	}
	if report.Resources.PeakPostgresConnections > postgresConnectionLimit {
		failures = append(failures, "PostgreSQL connections exceeded the configured aggregate bound")
	}
	for application, maximum := range map[string]int64{
		"capacity-gateway-first": 24, "capacity-gateway-second": 24, "capacity-observer": 1,
	} {
		if report.Resources.PeakPostgresByApplication[application] > maximum {
			failures = append(failures, application+": PostgreSQL pool exceeded its owner bound")
		}
	}
	if report.Resources.ValkeyP95Milliseconds > 25 {
		failures = append(failures, "Valkey p95 exceeded 25 ms")
	}
	if report.Database.Users != int64(report.Users) {
		failures = append(failures, "database user fixture count drifted")
	}
	expectedHolds := int64(0)
	if len(report.Faults) > 0 {
		expectedHolds = 4
	}
	if report.Database.NonTerminalRequests != 0 || report.Database.ReservedHolds != expectedHolds {
		failures = append(failures, "request or reservation terminal/hold facts drifted")
	}
	failures = append(failures, validateFaults(report.Faults)...)
	return failures
}

func durationPercentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * quantile)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}
