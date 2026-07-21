package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/redis/go-redis/v9"
)

func captureConfigurationRevision(t *testing.T, service *configuration.Service, actor identity.Principal, requestID string) configuration.Revision {
	t.Helper()
	revision, err := service.CreateRevision(context.Background(), actor, mutationRequest(requestID))
	if err != nil {
		t.Fatalf("CreateRevision(%s) error = %v", requestID, err)
	}
	return revision
}

func publishConfigurationRevision(t *testing.T, service *configuration.Service, actor identity.Principal, revisionID uuid.UUID, expectedVersion int64, action configuration.MutationAction, requestID string) configuration.Active {
	t.Helper()
	active, err := service.Publish(context.Background(), actor, revisionID, expectedVersion, action, mutationRequest(requestID))
	if err != nil {
		t.Fatalf("Publish(%s) error = %v", requestID, err)
	}
	return active
}

func mutationRequest(requestID string) configuration.MutationRequest {
	return configuration.MutationRequest{IdempotencyKey: uuid.New(), RequestID: requestID}
}

type liveConfigurationState struct {
	providerID, modelID, credentialID                   uuid.UUID
	providerName, baseURL                               string
	publicName, upstreamName, displayName, capabilities string
	rpmLimit, concurrencyLimit, priority, weight        int32
	tpmLimit                                            int64
}

func setLiveConfigurationState(t *testing.T, pool *pgxpool.Pool, state liveConfigurationState) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin live registry state change: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "UPDATE providers SET name = $2, base_url = $3 WHERE id = $1", state.providerID, state.providerName, state.baseURL); err != nil {
		t.Fatalf("update live Provider: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE models
SET public_name = $2, upstream_name = $3, display_name = $4, capabilities = $5::jsonb
WHERE id = $1`, state.modelID, state.publicName, state.upstreamName, state.displayName, state.capabilities); err != nil {
		t.Fatalf("update live model: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE provider_credentials
SET rpm_limit = $2, tpm_limit = $3, concurrency_limit = $4
WHERE id = $1`, state.credentialID, state.rpmLimit, state.tpmLimit, state.concurrencyLimit); err != nil {
		t.Fatalf("update live credential: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE credential_models SET priority = $3, weight = $4
WHERE credential_id = $1 AND model_id = $2`, state.credentialID, state.modelID, state.priority, state.weight); err != nil {
		t.Fatalf("update live route: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit live registry state change: %v", err)
	}
}

func assertPublishedCatalogA(t *testing.T, repository *ConfigurationRepository) {
	t.Helper()
	active, catalog, err := repository.ActiveCatalog(context.Background())
	if err != nil {
		t.Fatalf("ActiveCatalog() error = %v", err)
	}
	if active.Revision.ID == uuid.Nil || active.Version < 1 {
		t.Fatalf("ActiveCatalog() active = %#v", active)
	}
	assertCatalogSummary(t, catalogSummary(catalog), 1, 1, 1, 1)
	if catalog.Providers[0].Name != "Provider A" || catalog.Providers[0].BaseURL != "https://198.18.0.30/v1" {
		t.Fatalf("published Provider snapshot = %#v", catalog.Providers[0])
	}
	model := catalog.Models[0]
	var capabilities map[string]bool
	if err := json.Unmarshal(model.Capabilities, &capabilities); err != nil {
		t.Fatalf("decode published model capabilities: %v", err)
	}
	if model.PublicName != "model-a" || model.UpstreamName != "upstream-a" || model.DisplayName != "Model A" || !capabilities["chat"] || len(capabilities) != 1 {
		t.Fatalf("published model snapshot = %#v", model)
	}
	credential := catalog.Credentials[0]
	if credential.RPMLimit == nil || *credential.RPMLimit != 10 || credential.TPMLimit == nil || *credential.TPMLimit != 1000 || credential.ConcurrencyLimit == nil || *credential.ConcurrencyLimit != 2 {
		t.Fatalf("published credential snapshot = %#v", credential)
	}
	if catalog.Routes[0].Priority != 10 || catalog.Routes[0].Weight != 80 {
		t.Fatalf("published route snapshot = %#v", catalog.Routes[0])
	}
}

func assertCatalogSummary(t *testing.T, summary configuration.CatalogSummary, providers, models, credentials, routes int64) {
	t.Helper()
	if summary.ProviderCount != providers || summary.ModelCount != models || summary.CredentialCount != credentials || summary.RouteCount != routes {
		t.Fatalf("catalog summary = %#v, want %d/%d/%d/%d", summary, providers, models, credentials, routes)
	}
}

func countConfigurationFacts(t *testing.T, pool *pgxpool.Pool, query string, arguments ...any) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), query, arguments...).Scan(&count); err != nil {
		t.Fatalf("count configuration facts: %v", err)
	}
	return count
}

func assertConfigurationFactCount(t *testing.T, pool *pgxpool.Pool, query string, expected int, arguments ...any) {
	t.Helper()
	if actual := countConfigurationFacts(t, pool, query, arguments...); actual != expected {
		t.Fatalf("configuration fact count = %d, want %d", actual, expected)
	}
}

func cleanupConfigurationTestFacts(t *testing.T, pool *pgxpool.Pool, valkey *redis.Client, actorID, providerID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	statements := []struct {
		query     string
		arguments []any
	}{
		{query: "DELETE FROM active_config"},
		{query: "DELETE FROM config_mutations WHERE actor_user_id = $1", arguments: []any{actorID}},
		{query: "DELETE FROM audit_events WHERE actor_user_id = $1", arguments: []any{actorID}},
		{query: "DELETE FROM config_revisions WHERE created_by = $1", arguments: []any{actorID}},
		{query: "DELETE FROM providers WHERE id = $1", arguments: []any{providerID}},
		{query: "DELETE FROM users WHERE id = $1", arguments: []any{actorID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Errorf("configuration test cleanup %q: %v", statement.query, err)
		}
	}
	if err := valkey.FlushDB(ctx).Err(); err != nil {
		t.Errorf("clear isolated Valkey after configuration test: %v", err)
	}
}

func environmentValue(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
