package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/quota"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestRequestExecutionClaimFencesStaleWritersAndRecoveryHoldsReservation(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_TEST_DATABASE_URL is required for the request execution test")
		}
		t.Skip("isolated PostgreSQL is required for the request execution test")
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

	fixture := insertRequestExecutionFixture(t, pool)
	defer cleanupRequestExecutionFixture(t, pool, fixture)

	requestRepository := NewRequestRepository(&Connections{Postgres: pool})
	quotaRepository := NewQuotaRepository(&Connections{Postgres: pool})
	claimA, err := requestRepository.ClaimExecution(ctx, fixture.requestID, uuid.New())
	if err != nil {
		t.Fatalf("claim execution A: %v", err)
	}
	if !claimA.Valid() || claimA.Generation != 1 {
		t.Fatalf("claim A = %#v", claimA)
	}
	if _, err := requestRepository.ClaimExecution(ctx, fixture.requestID, uuid.New()); !errors.Is(err, execution.ErrNotClaimable) {
		t.Fatalf("claim execution B error = %v, want ErrNotClaimable", err)
	}
	attemptID, err := requestRepository.CreateAttempt(ctx, claimA, fixture.credentialID, 1)
	if err != nil {
		t.Fatalf("create fenced attempt: %v", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE requests SET execution_heartbeat_at = now() - interval '2 minutes' WHERE id = $1", fixture.requestID); err != nil {
		t.Fatalf("make execution stale: %v", err)
	}
	recovered, err := requestRepository.RecoverStaleExecutions(ctx, time.Now().UTC().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("recover stale execution: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered executions = %d, want 1", recovered)
	}

	var requestStatus, attemptStatus, reservationState string
	if err := pool.QueryRow(ctx, `SELECT r.status::text, a.status::text, lr.state::text
FROM requests r
JOIN request_attempts a ON a.request_id = r.id
JOIN ledger_reservations lr ON lr.request_id = r.id
WHERE r.id = $1 AND a.id = $2`, fixture.requestID, attemptID).Scan(&requestStatus, &attemptStatus, &reservationState); err != nil {
		t.Fatalf("read recovered facts: %v", err)
	}
	if requestStatus != "uncertain" || attemptStatus != "uncertain" || reservationState != "reserved" {
		t.Fatalf("recovered facts = request %s attempt %s reservation %s", requestStatus, attemptStatus, reservationState)
	}
	if err := requestRepository.HeartbeatExecution(ctx, claimA); !errors.Is(err, execution.ErrFenced) {
		t.Fatalf("stale heartbeat error = %v, want ErrFenced", err)
	}
	now := time.Now().UTC()
	if err := requestRepository.UpdateAttempt(ctx, claimA, attemptID, requestflow.AttemptUpdate{Status: "completed", CompletedAt: &now}); !errors.Is(err, execution.ErrFenced) {
		t.Fatalf("stale attempt completion error = %v, want ErrFenced", err)
	}
	if _, err := quotaRepository.Settle(ctx, fixture.requestID, claimA, 2, 3, quota.UsageSource(canonical.UsageAuthoritative)); !errors.Is(err, execution.ErrFenced) {
		t.Fatalf("stale settlement error = %v, want ErrFenced", err)
	}

	settlementFixture := insertRequestExecutionFixture(t, pool)
	defer cleanupRequestExecutionFixture(t, pool, settlementFixture)
	settlementClaim, err := requestRepository.ClaimExecution(ctx, settlementFixture.requestID, uuid.New())
	if err != nil {
		t.Fatalf("claim recoverable settlement: %v", err)
	}
	settlementAttemptID, err := requestRepository.CreateAttempt(ctx, settlementClaim, settlementFixture.credentialID, 1)
	if err != nil {
		t.Fatalf("create recoverable attempt: %v", err)
	}
	sentAt := time.Now().UTC()
	if err := requestRepository.UpdateAttempt(ctx, settlementClaim, settlementAttemptID, requestflow.AttemptUpdate{Status: "sending", SentAt: &sentAt}); err != nil {
		t.Fatalf("mark recoverable attempt sending: %v", err)
	}
	completedAt := time.Now().UTC()
	usage := requestflow.Usage{InputTokens: 7, OutputTokens: 2, Source: canonical.UsageAuthoritative}
	if err := requestRepository.UpdateAttempt(ctx, settlementClaim, settlementAttemptID, requestflow.AttemptUpdate{Status: "completed", CompletedAt: &completedAt, Usage: &usage}); err != nil {
		t.Fatalf("persist recoverable attempt usage: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE requests SET execution_heartbeat_at = now() - interval '2 minutes' WHERE id = $1", settlementFixture.requestID); err != nil {
		t.Fatalf("make settlement execution stale: %v", err)
	}
	if recovered, err := requestRepository.RecoverStaleExecutions(ctx, time.Now().UTC().Add(-time.Minute), 10); err != nil || recovered != 0 {
		t.Fatalf("known usage stale recovery = %d, %v; want deferred settlement", recovered, err)
	}
	recoverable, err := requestRepository.ListRecoverableSettlements(ctx, time.Now().UTC().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("list recoverable settlements: %v", err)
	}
	if len(recoverable) != 1 || recoverable[0].Claim != settlementClaim || recoverable[0].Usage != usage {
		t.Fatalf("recoverable settlements = %#v", recoverable)
	}
	if _, err := quotaRepository.Settle(ctx, settlementFixture.requestID, settlementClaim, usage.InputTokens, usage.OutputTokens, quota.UsageSource(usage.Source)); err != nil {
		t.Fatalf("recover persisted usage settlement: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT r.status::text, lr.state::text FROM requests r JOIN ledger_reservations lr ON lr.request_id = r.id WHERE r.id = $1`, settlementFixture.requestID).Scan(&requestStatus, &reservationState); err != nil {
		t.Fatalf("read recovered settlement facts: %v", err)
	}
	if requestStatus != "completed" || reservationState != "settled" {
		t.Fatalf("recovered settlement facts = request %s reservation %s", requestStatus, reservationState)
	}

	queuedFixture := insertRequestExecutionFixture(t, pool)
	defer cleanupRequestExecutionFixture(t, pool, queuedFixture)
	if _, err := pool.Exec(ctx, "UPDATE requests SET updated_at = now() - interval '2 minutes' WHERE id = $1", queuedFixture.requestID); err != nil {
		t.Fatalf("make queued request stale: %v", err)
	}
	staleQueued, err := requestRepository.ListStaleQueuedRequests(ctx, time.Now().UTC().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("list stale queued requests: %v", err)
	}
	if len(staleQueued) != 1 || staleQueued[0] != queuedFixture.requestID {
		t.Fatalf("stale queued requests = %#v", staleQueued)
	}
	if _, err := quotaRepository.ReleaseAccepted(ctx, queuedFixture.requestID, "execution_abandoned", "no execution claimed the request"); err != nil {
		t.Fatalf("release stale queued request: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT r.status::text, lr.state::text FROM requests r JOIN ledger_reservations lr ON lr.request_id = r.id WHERE r.id = $1`, queuedFixture.requestID).Scan(&requestStatus, &reservationState); err != nil {
		t.Fatalf("read released queued facts: %v", err)
	}
	if requestStatus != "failed" || reservationState != "released" {
		t.Fatalf("released queued facts = request %s reservation %s", requestStatus, reservationState)
	}
}

type requestExecutionFixture struct {
	userID, providerID, requestID, credentialID uuid.UUID
}

func insertRequestExecutionFixture(t *testing.T, pool *pgxpool.Pool) requestExecutionFixture {
	t.Helper()
	ctx := context.Background()
	fixture := requestExecutionFixture{userID: uuid.New(), providerID: uuid.New(), requestID: uuid.New(), credentialID: uuid.New()}
	keyID, modelID, entitlementID, reserveEventID, reservationID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	keyDigest := sha256.Sum256(fixture.userID[:])
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at)
VALUES ($1, $2, 'Execution Test Member', 'fixture-hash', 'member', 'active', now())`, fixture.userID, "execution-"+fixture.userID.String()+"@example.test"); err != nil {
		t.Fatalf("insert execution user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO gateway_keys (id, user_id, name, prefix, secret_digest)
VALUES ($1, $2, 'Execution Test Key', $3, $4)`, keyID, fixture.userID, "gw_execution_", keyDigest[:]); err != nil {
		t.Fatalf("insert execution key: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO providers (id, slug, name, kind, base_url, enabled)
VALUES ($1, $2, 'Execution Provider', 'openai-compatible', 'https://example.test/v1', true)`, fixture.providerID, "execution-"+fixture.providerID.String()); err != nil {
		t.Fatalf("insert execution Provider: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO models (id, provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, enabled)
VALUES ($1, $2, $3, 'execution-upstream', 'Execution Model', 'free', '{"chat":true}', true)`, modelID, fixture.providerID, "execution-"+modelID.String()); err != nil {
		t.Fatalf("insert execution model: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO provider_credentials (id, provider_id, name, encrypted_secret, resource_domain, status)
VALUES ($1, $2, 'Execution Credential', $3, 'free', 'active')`, fixture.credentialID, fixture.providerID, []byte("fixture-secret")); err != nil {
		t.Fatalf("insert execution credential: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO entitlements (id, user_id, plan, resource_domain, model_id, granted_tokens, starts_at, expires_at, concurrency_limit)
VALUES ($1, $2, 'token', 'free', $3, 1000, now() - interval '1 hour', now() + interval '1 day', 1)`, entitlementID, fixture.userID, modelID); err != nil {
		t.Fatalf("insert execution entitlement: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO requests (id, request_digest, user_id, gateway_key_id, model_id, entitlement_id, resource_domain, status, stream)
VALUES ($1, $2, $3, $4, $5, $6, 'free', 'queued', false)`, fixture.requestID, make([]byte, 32), fixture.userID, keyID, modelID, entitlementID); err != nil {
		t.Fatalf("insert execution request: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_events (id, user_id, entitlement_id, request_id, reservation_id, kind, token_delta, reserved_tokens, usage_source, source_event_id)
VALUES ($1, $2, $3, $4, $5, 'reservation', -100, 100, 'estimated', $5)`, reserveEventID, fixture.userID, entitlementID, fixture.requestID, reservationID); err != nil {
		t.Fatalf("insert execution reservation event: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_reservations (id, entitlement_id, request_id, state, reserved_tokens, reserve_event_id)
VALUES ($1, $2, $3, 'reserved', 100, $4)`, reservationID, entitlementID, fixture.requestID, reserveEventID); err != nil {
		t.Fatalf("insert execution reservation: %v", err)
	}
	return fixture
}

func cleanupRequestExecutionFixture(t *testing.T, pool *pgxpool.Pool, fixture requestExecutionFixture) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []struct {
		query string
		id    uuid.UUID
	}{
		{query: "DELETE FROM ledger_reservations WHERE request_id = $1", id: fixture.requestID},
		{query: "DELETE FROM ledger_events WHERE request_id = $1", id: fixture.requestID},
		{query: "DELETE FROM requests WHERE id = $1", id: fixture.requestID},
		{query: "DELETE FROM providers WHERE id = $1", id: fixture.providerID},
		{query: "DELETE FROM users WHERE id = $1", id: fixture.userID},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.id); err != nil {
			t.Errorf("cleanup request execution fixture: %v", err)
		}
	}
}
