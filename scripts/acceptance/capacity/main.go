package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		baseURLValue            = flag.String("base-urls", "", "comma-separated isolated Gateway base URLs")
		databaseURL             = flag.String("database-url", "", "isolated PostgreSQL URL")
		valkeyAddress           = flag.String("valkey-address", "", "isolated Valkey address")
		valkeyPassword          = flag.String("valkey-password", "", "isolated Valkey password")
		apiKeyPepper            = flag.String("api-key-pepper", "", "test-only API key HMAC pepper")
		modelIDValue            = flag.String("model-id", "", "published model UUID")
		planVersionIDValue      = flag.String("plan-version-id", "", "published service plan version UUID")
		model                   = flag.String("model", "capacity-chat", "published model alias")
		providerAdminURL        = flag.String("provider-admin-url", "", "isolated Provider fixture admin URL")
		userCount               = flag.Int("users", 300, "number of distinct controlled users")
		activeUsers             = flag.Int("active-users", 60, "number of steady active users")
		duration                = flag.Duration("duration", time.Minute, "steady phase duration")
		postgresConnectionLimit = flag.Int64("postgres-connection-limit", 49, "aggregate Gateway pools plus observer")
	)
	flag.Parse()
	baseURLs := splitURLs(*baseURLValue)
	modelID, modelErr := uuid.Parse(*modelIDValue)
	planVersionID, planVersionErr := uuid.Parse(*planVersionIDValue)
	if len(baseURLs) == 0 || *databaseURL == "" || *valkeyAddress == "" || len(*apiKeyPepper) < 32 || modelErr != nil || planVersionErr != nil || *providerAdminURL == "" || *userCount < 200 || *userCount > 300 || *activeUsers < 1 || *activeUsers > *userCount || *duration < 10*time.Second {
		fmt.Fprintln(os.Stderr, "capacity acceptance arguments are invalid")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *duration+2*time.Minute)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(*databaseURL)
	if err != nil {
		fatal(err)
	}
	poolConfig.MaxConns = 1
	poolConfig.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()
	runID := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	users, err := provisionUsers(ctx, pool, []byte(*apiKeyPepper), modelID, planVersionID, *userCount, runID)
	if err != nil {
		fatal(err)
	}
	client := newLoadClient(baseURLs, *model)
	defer client.close()
	sampler := newResourceSampler(baseURLs, pool, *valkeyAddress, *valkeyPassword, 0)
	defer sampler.close()
	sampleContext, stopSampling := context.WithCancel(ctx)
	ready := make(chan struct{})
	go sampler.run(sampleContext, ready)
	<-ready
	collector := &resultCollector{}
	startedAt := time.Now().UTC()
	runConcurrent(ctx, client, collector, "warmup", users[:min(20, len(users))], func(int) requestKind { return kindShort })
	runSteady(ctx, client, collector, users[:*activeUsers], *duration)
	runConcurrent(ctx, client, collector, "extended_stream", users[:*activeUsers], func(int) requestKind { return kindExtendedStream })
	runConcurrent(ctx, client, collector, "burst", users, func(int) requestKind { return kindShort })
	hotspot := make([]virtualUser, 32)
	for index := range hotspot {
		hotspot[index] = users[0]
	}
	runConcurrent(ctx, client, collector, "hotspot", hotspot, func(int) requestKind { return kindLongStream })
	faults, err := runFaults(ctx, client, pool, users[1], *providerAdminURL)
	if err != nil {
		fatal(err)
	}
	client.close()
	time.Sleep(3 * time.Second)
	sampler.sample(ctx)
	stopSampling()
	database, err := summarizeDatabase(ctx, pool, runID)
	if err != nil {
		fatal(err)
	}
	provider, err := readProviderStats(ctx, *providerAdminURL)
	if err != nil {
		fatal(err)
	}
	report := capacityReport{
		RunID: runID, StartedAt: startedAt, DurationSeconds: int64(time.Since(startedAt).Seconds()),
		Users: *userCount, ActiveUsers: *activeUsers, Phases: summarizeResults(collector.results),
		Resources: sampler.report(), Database: database, Provider: provider, Faults: faults,
	}
	report.Failures = validateReport(report, *postgresConnectionLimit)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fatal(err)
	}
	if len(report.Failures) > 0 {
		os.Exit(1)
	}
}

func splitURLs(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimRight(strings.TrimSpace(item), "/"); strings.HasPrefix(item, "http://127.0.0.1:") {
			result = append(result, item)
		}
	}
	return result
}

func readProviderStats(ctx context.Context, baseURL string) (map[string]int64, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/stats", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read Provider stats: status %d: %w", response.StatusCode, err)
	}
	var value map[string]int64
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
