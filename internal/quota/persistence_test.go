package quota_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/quota"
	"github.com/luckymaomi/llmgateway/internal/store"
	"github.com/luckymaomi/llmgateway/migrations"
)

var quotaTestDatabaseURL = flag.String("quota-test-database-url", "", "isolated PostgreSQL URL for quota persistence tests")

func TestPersistentSubscriptionQuotaIsIdempotentAndAtomic(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = *quotaTestDatabaseURL
	}
	if databaseURL == "" {
		t.Skip("LLMGATEWAY_TEST_DATABASE_URL is required for the PostgreSQL quota test")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	if err := migrations.Up(ctx, database); err != nil {
		t.Fatalf("migrations.Up() error = %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()

	fixture := seedQuotaFixture(t, ctx, pool)
	repository := store.NewQuotaRepository(&store.Connections{Postgres: pool})
	requestRepository := store.NewRequestRepository(&store.Connections{Postgres: pool})

	digest := sha256.Sum256([]byte("first logical request"))
	idempotencyKey := "first-request"
	accepted, err := repository.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: uuid.New(), UserID: fixture.userID, GatewayKeyID: fixture.gatewayKeyID,
		ModelID: fixture.modelID, RequestDigest: digest[:], IdempotencyKey: &idempotencyKey, ReservedTokens: 70,
	})
	if err != nil {
		t.Fatalf("AcceptRequest() error = %v", err)
	}
	if accepted.Request.SubscriptionID != fixture.subscriptionID || accepted.Request.ResourcePoolID != fixture.resourcePoolID {
		t.Fatalf("accepted request escaped its subscription route: %#v", accepted.Request)
	}
	replayed, err := repository.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: uuid.New(), UserID: fixture.userID, GatewayKeyID: fixture.gatewayKeyID,
		ModelID: fixture.modelID, RequestDigest: digest[:], IdempotencyKey: &idempotencyKey, ReservedTokens: 70,
	})
	if err != nil {
		t.Fatalf("AcceptRequest(replay) error = %v", err)
	}
	if !replayed.Replayed || replayed.Request.ID != accepted.Request.ID || replayed.Reservation.ID != accepted.Reservation.ID {
		t.Fatalf("replayed = %#v, accepted = %#v", replayed, accepted)
	}

	claim, err := requestRepository.ClaimExecution(ctx, accepted.Request.ID, uuid.New())
	if err != nil {
		t.Fatalf("ClaimExecution() error = %v", err)
	}
	settled, err := repository.Settle(ctx, accepted.Request.ID, claim, 20, 10, quota.UsageAuthoritative)
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	repeated, err := repository.Settle(ctx, accepted.Request.ID, claim, 20, 10, quota.UsageAuthoritative)
	if err != nil {
		t.Fatalf("Settle(replay) error = %v", err)
	}
	if repeated.Reservation.TerminalEventID == nil || *repeated.Reservation.TerminalEventID != *settled.Reservation.TerminalEventID {
		t.Fatalf("settlement replay appended a different terminal fact: %#v", repeated)
	}
	if settled.Request.TotalCostNanos == nil || *settled.Request.TotalCostNanos != 165_000 {
		t.Fatalf("settled request cost snapshot = %#v", settled.Request)
	}
	if _, err := repository.Compensate(ctx, accepted.Request.ID, claim, 20, 10, quota.UsageAuthoritative, "partial_stream", "already settled"); !errors.Is(err, quota.ErrTerminalConflict) {
		t.Fatalf("Compensate(after settle) error = %v, want ErrTerminalConflict", err)
	}
	ledger, err := repository.ListLedger(ctx, quota.LedgerFilter{SubscriptionID: &fixture.subscriptionID, Page: quota.Page{Size: 20}})
	if err != nil {
		t.Fatalf("ListLedger() error = %v", err)
	}
	if len(ledger.Items) != 3 {
		t.Fatalf("ledger entry count = %d, want grant + reservation + settlement", len(ledger.Items))
	}

	testConcurrentReservations(t, ctx, repository, fixture)
}

func testConcurrentReservations(t *testing.T, ctx context.Context, repository *store.QuotaRepository, fixture quotaFixture) {
	const workers = 100
	start := make(chan struct{})
	var completed sync.WaitGroup
	var acceptedCount atomic.Int32
	var exhaustedCount atomic.Int32
	errorsFound := make(chan error, workers)
	completed.Add(workers)
	for index := 0; index < workers; index++ {
		go func(index int) {
			defer completed.Done()
			<-start
			digest := sha256.Sum256([]byte(fmt.Sprintf("concurrent-%03d", index)))
			_, err := repository.AcceptRequest(ctx, quota.AcceptInput{
				RequestID: uuid.New(), UserID: fixture.concurrentUserID, GatewayKeyID: fixture.concurrentGatewayKeyID,
				ModelID: fixture.modelID, RequestDigest: digest[:], ReservedTokens: 20,
			})
			switch {
			case err == nil:
				acceptedCount.Add(1)
			case errors.Is(err, quota.ErrQuotaExhausted):
				exhaustedCount.Add(1)
			default:
				errorsFound <- err
			}
		}(index)
	}
	close(start)
	completed.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent AcceptRequest() error = %v", err)
	}
	if acceptedCount.Load() != 50 || exhaustedCount.Load() != 50 {
		t.Fatalf("accepted = %d, exhausted = %d, want 50/50", acceptedCount.Load(), exhaustedCount.Load())
	}
}

type quotaFixture struct {
	adminID, userID, concurrentUserID        uuid.UUID
	providerID, modelID, resourcePoolID      uuid.UUID
	gatewayKeyID, concurrentGatewayKeyID     uuid.UUID
	planID, versionID                        uuid.UUID
	subscriptionID, concurrentSubscriptionID uuid.UUID
}

func seedQuotaFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) quotaFixture {
	t.Helper()
	f := quotaFixture{
		adminID: uuid.New(), userID: uuid.New(), concurrentUserID: uuid.New(), providerID: uuid.New(), modelID: uuid.New(),
		resourcePoolID: uuid.New(), gatewayKeyID: uuid.New(), concurrentGatewayKeyID: uuid.New(), planID: uuid.New(),
		versionID: uuid.New(), subscriptionID: uuid.New(), concurrentSubscriptionID: uuid.New(),
	}
	suffix := uuid.NewString()
	mustExec(t, ctx, pool, `INSERT INTO users (id, email, display_name, password_hash, role, status) VALUES
  ($1, $2, 'Quota Admin', 'hash', 'administrator', 'active'),
  ($3, $4, 'Quota User', 'hash', 'member', 'active'),
  ($5, $6, 'Concurrent User', 'hash', 'member', 'active')`,
		f.adminID, "quota-admin-"+suffix+"@example.com", f.userID, "quota-user-"+suffix+"@example.com", f.concurrentUserID, "quota-concurrent-"+suffix+"@example.com")
	mustExec(t, ctx, pool, `INSERT INTO providers (id, catalog_id, slug, name, kind, base_url, source_url, verified_at)
VALUES ($1, $2, $3, 'Quota Provider', 'openai-compatible', 'https://example.com/v1', 'https://example.com/docs', now())`, f.providerID, "quota-catalog-"+suffix, "quota-provider-"+suffix)
	mustExec(t, ctx, pool, `INSERT INTO models (id, provider_id, public_name, upstream_name, display_name, capabilities)
VALUES ($1, $2, $3, 'quota-upstream', 'Quota Model', '{"chat":true}')`, f.modelID, f.providerID, "quota-model-"+suffix)
	mustExec(t, ctx, pool, `INSERT INTO resource_pools (id, provider_id, slug, name) VALUES ($1, $2, $3, 'Quota Pool')`, f.resourcePoolID, f.providerID, "quota-pool-"+suffix)
	mustExec(t, ctx, pool, `INSERT INTO resource_pool_models (resource_pool_id, model_id) VALUES ($1, $2)`, f.resourcePoolID, f.modelID)
	mustExec(t, ctx, pool, `INSERT INTO model_price_versions
  (model_id, currency, input_rate_nanos_per_million, output_rate_nanos_per_million, effective_at, created_by)
VALUES ($1, 'USD', 3250000000, 10000000000, now() - interval '1 hour', $2)`, f.modelID, f.adminID)
	mustExec(t, ctx, pool, `INSERT INTO service_plans (id, slug, name, kind, created_by) VALUES ($1, $2, 'Quota Plan', 'token', $3)`, f.planID, "quota-plan-"+suffix, f.adminID)
	mustExec(t, ctx, pool, `INSERT INTO service_plan_versions
  (id, service_plan_id, version, token_quota, validity_days, concurrency_limit, created_by)
VALUES ($1, $2, 1, 1000, 30, 100, $3)`, f.versionID, f.planID, f.adminID)
	mustExec(t, ctx, pool, `INSERT INTO service_plan_version_routes (service_plan_version_id, model_id, resource_pool_id) VALUES ($1, $2, $3)`, f.versionID, f.modelID, f.resourcePoolID)
	mustExec(t, ctx, pool, `UPDATE service_plans SET current_version_id = $1 WHERE id = $2`, f.versionID, f.planID)
	mustExec(t, ctx, pool, `INSERT INTO subscriptions
  (id, user_id, service_plan_version_id, status, granted_tokens, starts_at, expires_at, assigned_by) VALUES
  ($1, $2, $3, 'active', 100, now() - interval '1 hour', now() + interval '1 hour', $4),
  ($5, $6, $3, 'active', 1000, now() - interval '1 hour', now() + interval '1 hour', $4)`,
		f.subscriptionID, f.userID, f.versionID, f.adminID, f.concurrentSubscriptionID, f.concurrentUserID)
	mustExec(t, ctx, pool, `INSERT INTO ledger_events (user_id, subscription_id, kind, token_delta, usage_source, source_event_id, created_by) VALUES
  ($1, $2, 'grant', 100, 'unknown', $6, $5),
  ($3, $4, 'grant', 1000, 'unknown', $7, $5)`,
		f.userID, f.subscriptionID, f.concurrentUserID, f.concurrentSubscriptionID, f.adminID, uuid.New(), uuid.New())
	mustExec(t, ctx, pool, `INSERT INTO gateway_keys (id, user_id, name, prefix, secret_digest) VALUES
  ($1, $2, 'Quota Key', 'llmg_quota', $3),
  ($4, $5, 'Concurrent Key', 'llmg_concurrent', $6)`,
		f.gatewayKeyID, f.userID, []byte("quota-key-"+suffix), f.concurrentGatewayKeyID, f.concurrentUserID, []byte("concurrent-key-"+suffix))
	mustExec(t, ctx, pool, `INSERT INTO gateway_key_models (gateway_key_id, model_id) VALUES ($1, $2), ($3, $2)`, f.gatewayKeyID, f.modelID, f.concurrentGatewayKeyID)
	return f
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, statement string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, statement, args...); err != nil {
		t.Fatalf("seed quota fixture: %v", err)
	}
}
