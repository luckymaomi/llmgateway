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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/quota"
	"github.com/luckymaomi/llmgateway/internal/store"
	"github.com/luckymaomi/llmgateway/migrations"
)

var quotaTestDatabaseURL = flag.String("quota-test-database-url", "", "isolated PostgreSQL URL for quota persistence tests")

func TestPersistentQuotaLifecycleAndConcurrentReservations(t *testing.T) {
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
	t.Cleanup(func() { fixture.cleanup(context.Background(), pool) })
	repository := store.NewQuotaRepository(&store.Connections{Postgres: pool})
	requestRepository := store.NewRequestRepository(&store.Connections{Postgres: pool})

	now := time.Now().UTC()
	freeGrant := quota.NewEntitlement{
		IdempotencyKey: uuid.New(), RequestID: "quota-persistence-free", UserID: fixture.userID, Plan: quota.PlanToken, ResourceDomain: quota.ResourceFree, ModelID: &fixture.freeModelID,
		GrantedTokens: 100, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), ConcurrencyLimit: 2, Note: "free test allocation",
	}
	freeEntitlement, err := repository.CreateEntitlement(ctx, freeGrant, fixture.adminID)
	if err != nil {
		t.Fatalf("CreateEntitlement(free) error = %v", err)
	}
	replayedEntitlement, err := repository.CreateEntitlement(ctx, freeGrant, fixture.adminID)
	if err != nil {
		t.Fatalf("CreateEntitlement(free replay) error = %v", err)
	}
	if replayedEntitlement.ID != freeEntitlement.ID || replayedEntitlement.BalanceTokens != 100 {
		t.Fatalf("replayed entitlement = %#v, want original %#v", replayedEntitlement, freeEntitlement)
	}

	digest := sha256.Sum256([]byte("first logical request"))
	idempotencyKey := "first-request"
	firstRequestID := uuid.New()
	accepted, err := repository.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: firstRequestID, UserID: fixture.userID, GatewayKeyID: fixture.gatewayKeyID, ModelID: fixture.freeModelID,
		ConfigRevisionID: &fixture.configRevisionID, ResourceDomain: quota.ResourceFree, RequestDigest: digest[:], IdempotencyKey: &idempotencyKey, ReservedTokens: 70,
	})
	if err != nil {
		t.Fatalf("AcceptRequest() error = %v", err)
	}
	replayed, err := repository.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: uuid.New(), UserID: fixture.userID, GatewayKeyID: fixture.gatewayKeyID, ModelID: fixture.freeModelID,
		ConfigRevisionID: &fixture.configRevisionID, ResourceDomain: quota.ResourceFree, RequestDigest: digest[:], IdempotencyKey: &idempotencyKey, ReservedTokens: 70,
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
	repeatedSettlement, err := repository.Settle(ctx, accepted.Request.ID, claim, 20, 10, quota.UsageAuthoritative)
	if err != nil {
		t.Fatalf("Settle(replay) error = %v", err)
	}
	if repeatedSettlement.Reservation.TerminalEventID == nil || *repeatedSettlement.Reservation.TerminalEventID != *settled.Reservation.TerminalEventID {
		t.Fatalf("settlement replay appended a different terminal fact: %#v", repeatedSettlement)
	}
	if settled.Request.CostCurrency != "USD" || settled.Request.InputCostNanos == nil || *settled.Request.InputCostNanos != 65_000 ||
		settled.Request.OutputCostNanos == nil || *settled.Request.OutputCostNanos != 100_000 || settled.Request.TotalCostNanos == nil || *settled.Request.TotalCostNanos != 165_000 {
		t.Fatalf("settled request cost snapshot = %#v", settled.Request)
	}
	if _, err := repository.Compensate(ctx, accepted.Request.ID, claim, 20, 10, quota.UsageAuthoritative, "partial_stream", "already settled"); !errors.Is(err, quota.ErrTerminalConflict) {
		t.Fatalf("Compensate(after settle) error = %v, want ErrTerminalConflict", err)
	}

	entitlementPage, err := repository.ListEntitlements(ctx, quota.EntitlementQuery{
		UserID: &fixture.userID,
		Page:   quota.Page{Size: 20},
	})
	if err != nil {
		t.Fatalf("ListEntitlements() error = %v", err)
	}
	if balanceFor(entitlementPage.Items, freeEntitlement.ID) != 70 {
		t.Fatalf("free entitlement balance = %d, want 70", balanceFor(entitlementPage.Items, freeEntitlement.ID))
	}
	ledgerPage, err := repository.ListLedger(ctx, quota.LedgerFilter{EntitlementID: &freeEntitlement.ID, Page: quota.Page{Size: 20}})
	if err != nil {
		t.Fatalf("ListLedger() error = %v", err)
	}
	if len(ledgerPage.Items) != 3 {
		t.Fatalf("ledger entry count = %d, want grant + reservation + settlement", len(ledgerPage.Items))
	}

	professionalEntitlement, err := repository.CreateEntitlement(ctx, quota.NewEntitlement{
		IdempotencyKey: uuid.New(), RequestID: "quota-persistence-professional", UserID: fixture.userID, Plan: quota.PlanCoding, ResourceDomain: quota.ResourceProfessional, ModelID: &fixture.professionalModelID,
		GrantedTokens: 1_000, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), ConcurrencyLimit: 2, Note: "professional test allocation",
	}, fixture.adminID)
	if err != nil {
		t.Fatalf("CreateEntitlement(professional) error = %v", err)
	}
	secondDigest := sha256.Sum256([]byte("free pool must stay isolated"))
	if _, err := repository.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: uuid.New(), UserID: fixture.userID, GatewayKeyID: fixture.gatewayKeyID, ModelID: fixture.freeModelID,
		ConfigRevisionID: &fixture.configRevisionID, ResourceDomain: quota.ResourceFree, RequestDigest: secondDigest[:], ReservedTokens: 100,
	}); !errors.Is(err, quota.ErrQuotaExhausted) {
		t.Fatalf("free AcceptRequest() error = %v, want ErrQuotaExhausted", err)
	}
	if _, err := repository.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: uuid.New(), UserID: fixture.userID, GatewayKeyID: fixture.gatewayKeyID, ModelID: fixture.freeModelID,
		ConfigRevisionID: &fixture.configRevisionID, ResourceDomain: quota.ResourceProfessional, RequestDigest: secondDigest[:], ReservedTokens: 10,
	}); !errors.Is(err, quota.ErrResourceDomainMismatch) {
		t.Fatalf("cross-domain AcceptRequest() error = %v, want ErrResourceDomainMismatch", err)
	}
	if balanceFor(mustListEntitlements(t, ctx, repository, fixture.userID), professionalEntitlement.ID) != 1_000 {
		t.Fatal("a free-domain failure consumed the professional entitlement")
	}

	testConcurrentPersistentReservations(t, ctx, repository, fixture, now)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := pool.Exec(ctx, "DELETE FROM gateway_key_models WHERE gateway_key_id = $1 AND model_id = $2", fixture.gatewayKeyID, fixture.freeModelID); err != nil {
			t.Fatalf("remove gateway key model binding (attempt %d): %v", attempt+1, err)
		}
	}
	revokedDigest := sha256.Sum256([]byte("revoked model request"))
	if _, err := repository.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: uuid.New(), UserID: fixture.userID, GatewayKeyID: fixture.gatewayKeyID, ModelID: fixture.freeModelID,
		ConfigRevisionID: &fixture.configRevisionID, ResourceDomain: quota.ResourceFree, RequestDigest: revokedDigest[:], ReservedTokens: 1,
	}); !errors.Is(err, quota.ErrModelNotAuthorized) {
		t.Fatalf("AcceptRequest(revoked model) error = %v, want ErrModelNotAuthorized", err)
	}
}

func testConcurrentPersistentReservations(t *testing.T, ctx context.Context, repository *store.QuotaRepository, fixture quotaFixture, now time.Time) {
	if _, err := repository.CreateEntitlement(ctx, quota.NewEntitlement{
		IdempotencyKey: uuid.New(), RequestID: "quota-persistence-concurrent", UserID: fixture.concurrentUserID, Plan: quota.PlanToken, ResourceDomain: quota.ResourceFree, ModelID: &fixture.freeModelID,
		GrantedTokens: 1_000, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), ConcurrencyLimit: 100, Note: "concurrent reservation allocation",
	}, fixture.adminID); err != nil {
		t.Fatalf("CreateEntitlement(concurrent) error = %v", err)
	}
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
				RequestID: uuid.New(), UserID: fixture.concurrentUserID, GatewayKeyID: fixture.concurrentGatewayKeyID, ModelID: fixture.freeModelID,
				ConfigRevisionID: &fixture.configRevisionID, ResourceDomain: quota.ResourceFree, RequestDigest: digest[:], ReservedTokens: 20,
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
	adminID, userID, concurrentUserID            uuid.UUID
	providerID, freeModelID, professionalModelID uuid.UUID
	gatewayKeyID, concurrentGatewayKeyID         uuid.UUID
	configRevisionID                             uuid.UUID
}

func seedQuotaFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) quotaFixture {
	t.Helper()
	fixture := quotaFixture{
		adminID: uuid.New(), userID: uuid.New(), concurrentUserID: uuid.New(), providerID: uuid.New(),
		freeModelID: uuid.New(), professionalModelID: uuid.New(), gatewayKeyID: uuid.New(), concurrentGatewayKeyID: uuid.New(),
		configRevisionID: uuid.New(),
	}
	suffix := uuid.NewString()
	_, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at) VALUES
  ($1, $2, 'Quota Admin', 'hash', 'administrator', 'active', now()),
  ($3, $4, 'Quota User', 'hash', 'member', 'active', now()),
	  ($5, $6, 'Concurrent User', 'hash', 'member', 'active', now())`,
		fixture.adminID, "quota-admin-"+suffix+"@example.com", fixture.userID, "quota-user-"+suffix+"@example.com",
		fixture.concurrentUserID, "quota-concurrent-"+suffix+"@example.com",
	)
	if err != nil {
		t.Fatalf("seed quota users: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO providers (id, slug, name, kind, base_url, enabled) VALUES ($1, $2, 'Quota Provider', 'openai-compatible', 'https://example.com/v1', true)`, fixture.providerID, "quota-provider-"+suffix); err != nil {
		t.Fatalf("seed quota provider: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO models (id, provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, enabled) VALUES
  ($1, $2, $3, 'free-upstream', 'Free Model', 'free', '{"chat":true}', true),
  ($4, $2, $5, 'professional-upstream', 'Professional Model', 'professional', '{"chat":true}', true)`,
		fixture.freeModelID, fixture.providerID, "free-model-"+suffix, fixture.professionalModelID, "professional-model-"+suffix); err != nil {
		t.Fatalf("seed quota models: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO model_price_versions
  (model_id, currency, input_rate_nanos_per_million, output_rate_nanos_per_million, effective_at, created_by) VALUES
  ($1, 'USD', 3250000000, 10000000000, now() - interval '1 hour', $3),
  ($2, 'USD', 5000000000, 15000000000, now() - interval '1 hour', $3)`,
		fixture.freeModelID, fixture.professionalModelID, fixture.adminID); err != nil {
		t.Fatalf("seed quota model prices: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_keys (id, user_id, name, prefix, secret_digest) VALUES
  ($1, $2, 'Quota Key', 'llmg_quota', $3),
  ($4, $5, 'Concurrent Key', 'llmg_concurrent', $6)`,
		fixture.gatewayKeyID, fixture.userID, []byte("quota-key-"+suffix), fixture.concurrentGatewayKeyID, fixture.concurrentUserID, []byte("concurrent-key-"+suffix)); err != nil {
		t.Fatalf("seed quota gateway keys: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO config_revisions (id, checksum, created_by, published_at, published_by)
VALUES ($1, $2, $3, now(), $3)`, fixture.configRevisionID, "quota-revision-"+suffix, fixture.adminID); err != nil {
		t.Fatalf("seed published quota configuration: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO config_revision_providers (revision_id, provider_id, slug, name, kind, base_url)
VALUES ($1, $2, $3, 'Quota Provider', 'openai_compatible', 'https://example.com/v1')`, fixture.configRevisionID, fixture.providerID, "quota-provider-"+suffix); err != nil {
		t.Fatalf("seed published quota provider: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO config_revision_models
  (revision_id, model_id, provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, created_at) VALUES
  ($1, $2, $3, $4, 'free-upstream', 'Free Model', 'free', '{"chat":true}', now()),
  ($1, $5, $3, $6, 'professional-upstream', 'Professional Model', 'professional', '{"chat":true}', now())`,
		fixture.configRevisionID, fixture.freeModelID, fixture.providerID, "free-model-"+suffix, fixture.professionalModelID, "professional-model-"+suffix); err != nil {
		t.Fatalf("seed published quota models: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_key_models (gateway_key_id, model_id) VALUES
  ($1, $2), ($1, $3), ($4, $2)`, fixture.gatewayKeyID, fixture.freeModelID, fixture.professionalModelID, fixture.concurrentGatewayKeyID); err != nil {
		t.Fatalf("seed gateway key model bindings: %v", err)
	}
	return fixture
}

func (f quotaFixture) cleanup(ctx context.Context, pool *pgxpool.Pool) {
	userIDs := []uuid.UUID{f.adminID, f.userID, f.concurrentUserID}
	_, _ = pool.Exec(ctx, "DELETE FROM audit_events WHERE actor_user_id = ANY($1)", userIDs)
	_, _ = pool.Exec(ctx, "DELETE FROM ledger_reservations WHERE request_id IN (SELECT id FROM requests WHERE user_id = ANY($1))", userIDs)
	_, _ = pool.Exec(ctx, "DELETE FROM ledger_events WHERE user_id = ANY($1)", userIDs)
	_, _ = pool.Exec(ctx, "DELETE FROM requests WHERE user_id = ANY($1)", userIDs)
	_, _ = pool.Exec(ctx, "DELETE FROM entitlements WHERE user_id = ANY($1)", userIDs)
	_, _ = pool.Exec(ctx, "DELETE FROM gateway_keys WHERE user_id = ANY($1)", userIDs)
	_, _ = pool.Exec(ctx, "DELETE FROM config_revisions WHERE id = $1", f.configRevisionID)
	_, _ = pool.Exec(ctx, "DELETE FROM models WHERE provider_id = $1", f.providerID)
	_, _ = pool.Exec(ctx, "DELETE FROM providers WHERE id = $1", f.providerID)
	_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1)", userIDs)
}

func mustListEntitlements(t *testing.T, ctx context.Context, repository *store.QuotaRepository, userID uuid.UUID) []quota.Entitlement {
	t.Helper()
	result, err := repository.ListEntitlements(ctx, quota.EntitlementQuery{
		UserID: &userID,
		Page:   quota.Page{Size: 100},
	})
	if err != nil {
		t.Fatalf("ListEntitlements() error = %v", err)
	}
	return result.Items
}

func balanceFor(items []quota.Entitlement, id uuid.UUID) int64 {
	for _, item := range items {
		if item.ID == id {
			return item.BalanceTokens
		}
	}
	return -1
}
