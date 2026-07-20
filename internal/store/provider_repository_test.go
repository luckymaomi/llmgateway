package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestProviderMutationReconcilesCommittedResultAndReplaysOnce(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_CONTROL_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the Provider repository test")
		}
		t.Skip("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the isolated Provider repository test")
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
	t.Cleanup(pool.Close)

	actorID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at)
VALUES ($1, $2, 'Provider Test Admin', 'fixture-hash', 'administrator', 'active', now())`, actorID, "provider-repository-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatalf("insert Provider test actor: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM audit_events WHERE actor_user_id = $1", actorID); err != nil {
			t.Errorf("delete Provider test audits: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM provider_mutations WHERE actor_user_id = $1", actorID); err != nil {
			t.Errorf("delete Provider test mutations: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM providers WHERE slug = 'commit-reconciliation-fixture'"); err != nil {
			t.Errorf("delete Provider test record: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", actorID); err != nil {
			t.Errorf("delete Provider test actor: %v", err)
		}
	})

	fingerprint := sha256.Sum256([]byte("provider-create:commit-reconciliation-fixture"))
	mutation := registry.ProviderMutation{
		Action:             registry.ProviderMutationCreate,
		IdempotencyKey:     uuid.New(),
		RequestFingerprint: fingerprint[:],
		RequestID:          "provider-commit-reconciliation",
	}
	sourceURL := "https://source.example.test/provider"
	verifiedAt := time.Date(2026, time.July, 20, 8, 30, 0, 123000000, time.UTC)
	input := registry.Provider{
		Slug:       "commit-reconciliation-fixture",
		Name:       "Commit Reconciliation Fixture",
		Kind:       providers.KindOpenAICompatible,
		BaseURL:    "https://198.18.0.20/v1",
		Enabled:    false,
		SourceURL:  &sourceURL,
		VerifiedAt: &verifiedAt,
	}
	repository := NewRegistryRepository(&Connections{Postgres: pool})
	commitResponseLost := errors.New("fixture: commit response lost")
	commitCalls := 0
	repository.commitProviderMutation = func(ctx context.Context, tx pgx.Tx) error {
		commitCalls++
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return commitResponseLost
	}

	created, err := repository.CreateProvider(ctx, input, actorID, mutation)
	if err != nil {
		t.Fatalf("CreateProvider() after committed response loss error = %v", err)
	}
	if created.ID == uuid.Nil || created.Slug != input.Slug || created.Name != input.Name || created.Kind != input.Kind || created.BaseURL != input.BaseURL || created.Enabled || created.SourceURL == nil || *created.SourceURL != sourceURL || created.VerifiedAt == nil || !created.VerifiedAt.Equal(verifiedAt) {
		t.Fatalf("reconciled Provider = %#v", created)
	}
	if commitCalls != 1 {
		t.Fatalf("commit hook calls = %d, want 1", commitCalls)
	}
	assertProviderMutationFacts(t, pool, actorID, mutation, created)

	replayed, err := repository.CreateProvider(ctx, input, actorID, mutation)
	if err != nil {
		t.Fatalf("CreateProvider(replay) error = %v", err)
	}
	if replayed.ID != created.ID || replayed.Slug != created.Slug || replayed.Name != created.Name || replayed.Kind != created.Kind || replayed.BaseURL != created.BaseURL || replayed.Enabled != created.Enabled || !replayed.CreatedAt.Equal(created.CreatedAt) || !replayed.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("replayed Provider = %#v, want original %#v", replayed, created)
	}
	if commitCalls != 1 {
		t.Fatalf("replay invoked the commit hook; calls = %d, want 1", commitCalls)
	}
	assertProviderMutationFacts(t, pool, actorID, mutation, created)

	conflictingFingerprint := sha256.Sum256([]byte("provider-create:different-input"))
	conflictingMutation := mutation
	conflictingMutation.RequestFingerprint = conflictingFingerprint[:]
	conflictingMutation.RequestID = "provider-conflicting-replay"
	if _, err := repository.CreateProvider(ctx, input, actorID, conflictingMutation); !errors.Is(err, registry.ErrIdempotencyConflict) {
		t.Fatalf("CreateProvider(conflicting replay) error = %v, want ErrIdempotencyConflict", err)
	}
	if commitCalls != 1 {
		t.Fatalf("conflicting replay invoked the commit hook; calls = %d, want 1", commitCalls)
	}
	assertProviderMutationFacts(t, pool, actorID, mutation, created)
}

func TestProviderMutationConcurrentReplayCreatesOneFact(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_CONTROL_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the Provider repository test")
		}
		t.Skip("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the isolated Provider repository test")
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
	t.Cleanup(pool.Close)

	actorID := uuid.New()
	slug := "concurrent-replay-" + actorID.String()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at)
VALUES ($1, $2, 'Concurrent Provider Admin', 'fixture-hash', 'administrator', 'active', now())`, actorID, "provider-concurrent-"+actorID.String()+"@example.test"); err != nil {
		t.Fatalf("insert concurrent Provider test actor: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM audit_events WHERE actor_user_id = $1", actorID); err != nil {
			t.Errorf("delete concurrent Provider test audits: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM provider_mutations WHERE actor_user_id = $1", actorID); err != nil {
			t.Errorf("delete concurrent Provider test mutations: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM providers WHERE slug = $1", slug); err != nil {
			t.Errorf("delete concurrent Provider test record: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", actorID); err != nil {
			t.Errorf("delete concurrent Provider test actor: %v", err)
		}
	})

	fingerprint := sha256.Sum256([]byte("provider-create:" + slug))
	mutation := registry.ProviderMutation{
		Action:             registry.ProviderMutationCreate,
		IdempotencyKey:     uuid.New(),
		RequestFingerprint: fingerprint[:],
		RequestID:          "provider-concurrent-replay",
	}
	sourceURL := "https://source.example.test/concurrent-provider"
	verifiedAt := time.Date(2026, time.July, 20, 9, 0, 0, 456000000, time.UTC)
	input := registry.Provider{
		Slug: slug, Name: "Concurrent Replay Provider", Kind: providers.KindOpenAICompatible,
		BaseURL: "https://198.18.0.21/v1", Enabled: false, SourceURL: &sourceURL, VerifiedAt: &verifiedAt,
	}
	repository := NewRegistryRepository(&Connections{Postgres: pool})
	type outcome struct {
		provider registry.Provider
		err      error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			provider, err := repository.CreateProvider(ctx, input, actorID, mutation)
			outcomes <- outcome{provider: provider, err: err}
		}()
	}
	ready.Wait()
	close(start)

	first := <-outcomes
	second := <-outcomes
	for index, result := range []outcome{first, second} {
		if result.err != nil {
			t.Fatalf("concurrent CreateProvider() result %d error = %v", index+1, result.err)
		}
		if result.provider.ID == uuid.Nil {
			t.Fatalf("concurrent CreateProvider() result %d has no Provider ID", index+1)
		}
	}
	if first.provider.ID != second.provider.ID {
		t.Fatalf("concurrent Provider IDs = %s and %s, want one fact", first.provider.ID, second.provider.ID)
	}
	assertProviderMutationFacts(t, pool, actorID, mutation, first.provider)
}

func assertProviderMutationFacts(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID, mutation registry.ProviderMutation, provider registry.Provider) {
	t.Helper()
	ctx := context.Background()
	var providerCount, mutationCount, auditCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM providers WHERE slug = $1", provider.Slug).Scan(&providerCount); err != nil {
		t.Fatalf("count reconciled Providers: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM provider_mutations
WHERE actor_user_id = $1 AND action = $2 AND idempotency_key = $3`, actorID, mutation.Action, mutation.IdempotencyKey).Scan(&mutationCount); err != nil {
		t.Fatalf("count Provider mutations: %v", err)
	}
	var requestID string
	var encodedDetail []byte
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events
	WHERE actor_user_id = $1 AND target_type = 'provider' AND target_id = $2 AND action = 'provider.created'`, actorID, provider.ID.String()).Scan(&auditCount); err != nil {
		t.Fatalf("count reconciled Provider audits: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT request_id, detail FROM audit_events
	WHERE actor_user_id = $1 AND target_type = 'provider' AND target_id = $2 AND action = 'provider.created'`, actorID, provider.ID.String()).Scan(&requestID, &encodedDetail); err != nil {
		t.Fatalf("read reconciled Provider audit: %v", err)
	}
	if providerCount != 1 || mutationCount != 1 || auditCount != 1 {
		t.Fatalf("reconciled fact counts = Provider %d mutation %d audit %d, want 1/1/1", providerCount, mutationCount, auditCount)
	}
	if requestID != mutation.RequestID {
		t.Fatalf("Provider audit request_id = %q, want %q", requestID, mutation.RequestID)
	}
	var detail struct {
		Before map[string]any `json:"before"`
		After  map[string]any `json:"after"`
	}
	if err := json.Unmarshal(encodedDetail, &detail); err != nil {
		t.Fatalf("decode Provider audit detail: %v", err)
	}
	if detail.Before != nil || detail.After["slug"] != provider.Slug || detail.After["name"] != provider.Name || detail.After["kind"] != string(provider.Kind) || detail.After["base_url"] != provider.BaseURL || detail.After["enabled"] != provider.Enabled || detail.After["source_url"] != *provider.SourceURL || detail.After["verified_at"] != provider.VerifiedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("Provider audit detail = %#v", detail)
	}
}
