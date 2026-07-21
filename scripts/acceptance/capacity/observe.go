package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type resourceReport struct {
	BaselineGoroutines        int64            `json:"baselineGoroutines"`
	PeakGoroutines            int64            `json:"peakGoroutines"`
	FinalGoroutines           int64            `json:"finalGoroutines"`
	PeakResidentBytes         int64            `json:"peakResidentBytes"`
	FinalResidentBytes        int64            `json:"finalResidentBytes"`
	PeakPostgresConnections   int64            `json:"peakPostgresConnections"`
	PeakPostgresByApplication map[string]int64 `json:"peakPostgresByApplication"`
	ValkeyP95Milliseconds     float64          `json:"valkeyP95Milliseconds"`
}

type resourceSampler struct {
	baseURLs []string
	pool     *pgxpool.Pool
	valkey   *redis.Client
	client   *http.Client

	mu              sync.Mutex
	baseline        metricSample
	peak            metricSample
	latest          metricSample
	postgresByApp   map[string]int64
	valkeyLatencies []time.Duration
}

type metricSample struct {
	Goroutines          int64
	ResidentBytes       int64
	PostgresConnections int64
}

func newResourceSampler(baseURLs []string, pool *pgxpool.Pool, valkeyAddress, valkeyPassword string, valkeyDatabase int) *resourceSampler {
	return &resourceSampler{
		baseURLs: baseURLs, pool: pool, client: &http.Client{Timeout: 2 * time.Second},
		valkey:        redis.NewClient(&redis.Options{Addr: valkeyAddress, Password: valkeyPassword, DB: valkeyDatabase, DialTimeout: 2 * time.Second}),
		postgresByApp: make(map[string]int64),
	}
}

func (s *resourceSampler) close() { _ = s.valkey.Close() }

func (s *resourceSampler) run(ctx context.Context, ready chan<- struct{}) {
	s.sample(ctx)
	close(ready)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sample(ctx)
		}
	}
}

func (s *resourceSampler) sample(ctx context.Context) {
	value := metricSample{}
	for _, baseURL := range s.baseURLs {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/metrics", nil)
		response, err := s.client.Do(request)
		if err != nil {
			continue
		}
		metrics := parseProcessMetrics(response.Body)
		response.Body.Close()
		value.Goroutines += metrics.Goroutines
		value.ResidentBytes += metrics.ResidentBytes
	}
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND backend_type = 'client backend'`).Scan(&value.PostgresConnections)
	if rows, err := s.pool.Query(ctx, `SELECT COALESCE(NULLIF(application_name, ''), 'unspecified'), count(*)
FROM pg_stat_activity WHERE datname = current_database() AND backend_type = 'client backend' GROUP BY application_name`); err == nil {
		byApplication := make(map[string]int64)
		for rows.Next() {
			var name string
			var count int64
			if rows.Scan(&name, &count) == nil {
				byApplication[name] = count
			}
		}
		rows.Close()
		s.mu.Lock()
		for name, count := range byApplication {
			if count > s.postgresByApp[name] {
				s.postgresByApp[name] = count
			}
		}
		s.mu.Unlock()
	}
	started := time.Now()
	if err := s.valkey.Ping(ctx).Err(); err == nil {
		s.mu.Lock()
		s.valkeyLatencies = append(s.valkeyLatencies, time.Since(started))
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.baseline == (metricSample{}) {
		s.baseline = value
	}
	if value.Goroutines > s.peak.Goroutines {
		s.peak.Goroutines = value.Goroutines
	}
	if value.ResidentBytes > s.peak.ResidentBytes {
		s.peak.ResidentBytes = value.ResidentBytes
	}
	if value.PostgresConnections > s.peak.PostgresConnections {
		s.peak.PostgresConnections = value.PostgresConnections
	}
	s.latest = value
}

func (s *resourceSampler) report() resourceReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	latencies := append([]time.Duration(nil), s.valkeyLatencies...)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return resourceReport{
		BaselineGoroutines: s.baseline.Goroutines, PeakGoroutines: s.peak.Goroutines, FinalGoroutines: s.latest.Goroutines,
		PeakResidentBytes: s.peak.ResidentBytes, FinalResidentBytes: s.latest.ResidentBytes,
		PeakPostgresConnections: s.peak.PostgresConnections, ValkeyP95Milliseconds: durationPercentile(latencies, 0.95).Seconds() * 1000,
		PeakPostgresByApplication: copyCounts(s.postgresByApp),
	}
}

func copyCounts(source map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(source))
	for name, count := range source {
		result[name] = count
	}
	return result
}

func parseProcessMetrics(body interface{ Read([]byte) (int, error) }) metricSample {
	value := metricSample{}
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "go_goroutines ") {
			value.Goroutines, _ = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "go_goroutines ")), 10, 64)
		}
		if strings.HasPrefix(line, "process_resident_memory_bytes ") {
			parsed, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "process_resident_memory_bytes ")), 64)
			value.ResidentBytes = int64(parsed)
		}
	}
	return value
}

func (s metricSample) String() string {
	return fmt.Sprintf("goroutines=%d resident_bytes=%d postgres_connections=%d", s.Goroutines, s.ResidentBytes, s.PostgresConnections)
}
