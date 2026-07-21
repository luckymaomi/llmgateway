package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/quota"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestQuotaGrantMutationRecoversLostCommitAcknowledgementAndScopesIdempotencyByActor(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_CONTROL_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the quota grant repository test")
		}
		t.Skip("isolated PostgreSQL is required for the quota grant repository test")
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

	actorID, secondActorID, userID, providerID, modelID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at) VALUES
  ($1, $2, 'Quota Grant Administrator', 'hash', 'administrator', 'active', now()),
  ($3, $4, 'Second Quota Administrator', 'hash', 'administrator', 'active', now()),
  ($5, $6, 'Quota Grant Member', 'hash', 'member', 'active', now())`,
		actorID, "quota-grant-admin-"+actorID.String()+"@example.test",
		secondActorID, "quota-grant-admin-"+secondActorID.String()+"@example.test",
		userID, "quota-grant-member-"+userID.String()+"@example.test"); err != nil {
		t.Fatalf("seed quota grant users: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO providers (id, slug, name, kind, base_url, enabled)
VALUES ($1, $2, 'Quota Grant Provider', 'openai-compatible', 'https://example.com/v1', true)`, providerID, "quota-grant-"+providerID.String()); err != nil {
		t.Fatalf("seed quota grant Provider: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO models (id, provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, enabled)
VALUES ($1, $2, $3, 'quota-grant-upstream', 'Quota Grant Model', 'free', '{"chat":true}', true)`, modelID, providerID, "quota-grant-"+modelID.String()); err != nil {
		t.Fatalf("seed quota grant model: %v", err)
	}
	defer cleanupQuotaGrantFixture(t, pool, actorID, secondActorID, userID, modelID, providerID)

	repository := NewQuotaRepository(&Connections{Postgres: pool})
	idempotencyKey := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	input := quota.NewEntitlement{
		IdempotencyKey:   idempotencyKey,
		RequestID:        "quota-grant-lost-ack",
		UserID:           userID,
		Plan:             quota.PlanToken,
		ResourceDomain:   quota.ResourceFree,
		ModelID:          &modelID,
		GrantedTokens:    50_000,
		StartsAt:         now.Add(-time.Hour),
		ExpiresAt:        now.Add(30 * 24 * time.Hour),
		ConcurrencyLimit: 2,
		Note:             "production allocation",
	}
	repository.commitEntitlementMutation = func(commitCtx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(commitCtx); err != nil {
			return err
		}
		return errors.New("fixture lost commit acknowledgement")
	}
	created, err := repository.CreateEntitlement(ctx, input, actorID)
	if err != nil {
		t.Fatalf("CreateEntitlement(lost acknowledgement) error = %v", err)
	}
	repository.commitEntitlementMutation = func(commitCtx context.Context, tx pgx.Tx) error { return tx.Commit(commitCtx) }
	replayed, err := repository.CreateEntitlement(ctx, input, actorID)
	if err != nil {
		t.Fatalf("CreateEntitlement(replay) error = %v", err)
	}
	if replayed.ID != created.ID || replayed.BalanceTokens != input.GrantedTokens {
		t.Fatalf("replayed entitlement = %#v, created = %#v", replayed, created)
	}

	conflicting := input
	conflicting.GrantedTokens++
	if _, err := repository.CreateEntitlement(ctx, conflicting, actorID); !errors.Is(err, quota.ErrConflict) {
		t.Fatalf("CreateEntitlement(conflict) error = %v, want ErrConflict", err)
	}
	second := input
	second.RequestID = "quota-grant-second-actor"
	second.Note = "second administrator allocation"
	secondEntitlement, err := repository.CreateEntitlement(ctx, second, secondActorID)
	if err != nil {
		t.Fatalf("CreateEntitlement(second actor) error = %v", err)
	}
	if secondEntitlement.ID == created.ID {
		t.Fatal("actor-scoped idempotency reused another administrator's entitlement")
	}

	assertQuotaGrantFacts(t, pool, actorID, idempotencyKey, created.ID, "quota-grant-lost-ack")
	assertQuotaGrantFacts(t, pool, secondActorID, idempotencyKey, secondEntitlement.ID, "quota-grant-second-actor")
}

func cleanupQuotaGrantFixture(t *testing.T, pool *pgxpool.Pool, actorID, secondActorID, userID, modelID, providerID uuid.UUID) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{query: "DELETE FROM audit_events WHERE actor_user_id = ANY($1)", args: []any{[]uuid.UUID{actorID, secondActorID}}},
		{query: "DELETE FROM ledger_events WHERE user_id = $1", args: []any{userID}},
		{query: "DELETE FROM entitlements WHERE user_id = $1", args: []any{userID}},
		{query: "DELETE FROM models WHERE id = $1", args: []any{modelID}},
		{query: "DELETE FROM providers WHERE id = $1", args: []any{providerID}},
		{query: "DELETE FROM users WHERE id = ANY($1)", args: []any{[]uuid.UUID{actorID, secondActorID, userID}}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(context.Background(), statement.query, statement.args...); err != nil {
			t.Errorf("cleanup quota grant fixture: %v", err)
		}
	}
}

func assertQuotaGrantFacts(t *testing.T, pool *pgxpool.Pool, actorID, idempotencyKey, entitlementID uuid.UUID, requestID string) {
	t.Helper()
	var entitlementCount, grantCount, auditCount int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM entitlements WHERE id = $1", entitlementID).Scan(&entitlementCount); err != nil {
		t.Fatalf("count quota entitlement: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_events
WHERE entitlement_id = $1 AND kind = 'grant' AND created_by = $2 AND source_event_id = $3`, entitlementID, actorID, idempotencyKey).Scan(&grantCount); err != nil {
		t.Fatalf("count quota grant: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events
WHERE action = 'quota.entitlement_created' AND actor_user_id = $1 AND target_id = $2 AND request_id = $3`, actorID, entitlementID.String(), requestID).Scan(&auditCount); err != nil {
		t.Fatalf("count quota audit: %v", err)
	}
	if entitlementCount != 1 || grantCount != 1 || auditCount != 1 {
		t.Fatalf("quota grant facts = entitlement %d, grant %d, audit %d; want 1/1/1", entitlementCount, grantCount, auditCount)
	}
}
