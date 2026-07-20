package controlapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/security"
	"github.com/luckymaomi/llmgateway/internal/store"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestPersistentProviderControlLifecycle(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the isolated control API test")
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
	connections := &store.Connections{Postgres: pool}

	identityService, err := identity.NewService(store.NewIdentityRepository(pool), []byte("control-test-session-pepper-value-0001"), []byte("control-test-api-key-pepper-value-0001"))
	if err != nil {
		t.Fatalf("identity.NewService() error = %v", err)
	}
	envelope, err := security.NewEnvelopeCipher(1, map[uint32][]byte{1: bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatalf("security.NewEnvelopeCipher() error = %v", err)
	}
	allowedResolved := netip.MustParsePrefix("198.18.0.0/15")
	validator, err := security.NewURLValidator(security.SSRFPolicy{AllowedResolvedPrefixes: []netip.Prefix{allowedResolved}, MaxRedirects: 2})
	if err != nil {
		t.Fatalf("security.NewURLValidator() error = %v", err)
	}
	registryService, err := registry.NewService(store.NewRegistryRepository(connections), envelope, validator)
	if err != nil {
		t.Fatalf("registry.NewService() error = %v", err)
	}
	configurationService, err := configuration.NewService(store.NewConfigurationRepository(connections))
	if err != nil {
		t.Fatalf("configuration.NewService() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := New(identityService, registryService, configurationService, nil, config.Security{}, logger)
	router := chi.NewRouter()
	router.Mount("/api", api.Routes())
	server := httptest.NewServer(router)
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := &http.Client{Jar: jar}

	setup := controlRequest(t, client, http.MethodPost, server.URL+"/api/control/setup", "", map[string]any{
		"email": "admin@example.test", "displayName": "Admin User", "password": "correct horse battery staple",
	}, http.StatusCreated)
	var session sessionView
	decodeControlData(t, setup, &session)
	if session.CSRFToken == "" {
		t.Fatal("bootstrap did not return a CSRF token")
	}

	createInput := map[string]any{
		"slug": "fixture-provider", "name": "Fixture Provider", "kind": "openai-compatible", "baseUrl": "https://198.18.0.1/v1",
	}
	createdResponse := controlRequest(t, client, http.MethodPost, server.URL+"/api/control/providers", session.CSRFToken, createInput, http.StatusCreated)
	var created providerView
	decodeControlData(t, createdResponse, &created)
	if created.Slug != "fixture-provider" || created.Name != "Fixture Provider" || created.Status != "disabled" || created.UpdatedAt.IsZero() {
		t.Fatalf("created provider = %#v", created)
	}

	duplicate := controlRequest(t, client, http.MethodPost, server.URL+"/api/control/providers", session.CSRFToken, createInput, http.StatusConflict)
	requireControlProblem(t, duplicate, "conflict")

	invalidUpdates := []struct {
		name string
		body map[string]any
	}{
		{name: "immutable slug", body: map[string]any{
			"slug": "renamed-provider", "name": "Fixture Provider", "kind": "openai-compatible", "baseUrl": "https://198.18.0.2/v1", "expectedUpdatedAt": created.UpdatedAt,
		}},
		{name: "insecure scheme", body: map[string]any{
			"name": "Fixture Provider", "kind": "openai-compatible", "baseUrl": "http://198.18.0.2/v1", "expectedUpdatedAt": created.UpdatedAt,
		}},
		{name: "blocked address", body: map[string]any{
			"name": "Fixture Provider", "kind": "openai-compatible", "baseUrl": "https://127.0.0.1/v1", "expectedUpdatedAt": created.UpdatedAt,
		}},
		{name: "query", body: map[string]any{
			"name": "Fixture Provider", "kind": "openai-compatible", "baseUrl": "https://198.18.0.2/v1?region=test", "expectedUpdatedAt": created.UpdatedAt,
		}},
		{name: "empty query", body: map[string]any{
			"name": "Fixture Provider", "kind": "openai-compatible", "baseUrl": "https://198.18.0.2/v1?", "expectedUpdatedAt": created.UpdatedAt,
		}},
		{name: "fragment", body: map[string]any{
			"name": "Fixture Provider", "kind": "openai-compatible", "baseUrl": "https://198.18.0.2/v1#private", "expectedUpdatedAt": created.UpdatedAt,
		}},
	}
	for _, test := range invalidUpdates {
		t.Run(test.name, func(t *testing.T) {
			response := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID, session.CSRFToken, test.body, http.StatusBadRequest)
			requireControlProblem(t, response, "invalid_request")
		})
	}

	updatedResponse := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID, session.CSRFToken, map[string]any{
		"name": "Fixture Provider Updated", "kind": "deepseek", "baseUrl": "https://198.18.0.2/v1", "expectedUpdatedAt": created.UpdatedAt,
	}, http.StatusOK)
	var updated providerView
	decodeControlData(t, updatedResponse, &updated)
	if updated.Slug != created.Slug || updated.Name != "Fixture Provider Updated" || updated.Kind != "deepseek" || updated.BaseURL != "https://198.18.0.2/v1" || updated.Status != "disabled" || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated provider = %#v", updated)
	}

	staleUpdate := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID, session.CSRFToken, map[string]any{
		"name": "Stale Update", "kind": "deepseek", "baseUrl": "https://198.18.0.3/v1", "expectedUpdatedAt": created.UpdatedAt,
	}, http.StatusConflict)
	requireControlProblem(t, staleUpdate, "conflict")
	missingEnabled := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID+"/status", session.CSRFToken, map[string]any{
		"expectedUpdatedAt": updated.UpdatedAt,
	}, http.StatusBadRequest)
	requireControlProblem(t, missingEnabled, "invalid_request")

	enableResponse := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID+"/status", session.CSRFToken, map[string]any{
		"enabled": true, "expectedUpdatedAt": updated.UpdatedAt,
	}, http.StatusOK)
	var enabled providerView
	decodeControlData(t, enableResponse, &enabled)
	if enabled.Status != "enabled" || enabled.Name != updated.Name || enabled.Kind != updated.Kind || enabled.BaseURL != updated.BaseURL || !enabled.UpdatedAt.After(updated.UpdatedAt) {
		t.Fatalf("enabled provider = %#v", enabled)
	}

	staleStatus := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID+"/status", session.CSRFToken, map[string]any{
		"enabled": false, "expectedUpdatedAt": updated.UpdatedAt,
	}, http.StatusConflict)
	requireControlProblem(t, staleStatus, "conflict")

	disableResponse := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID+"/status", session.CSRFToken, map[string]any{
		"enabled": false, "expectedUpdatedAt": enabled.UpdatedAt,
	}, http.StatusOK)
	var disabled providerView
	decodeControlData(t, disableResponse, &disabled)
	if disabled.Status != "disabled" || disabled.Name != updated.Name || disabled.Kind != updated.Kind || disabled.BaseURL != updated.BaseURL || !disabled.UpdatedAt.After(enabled.UpdatedAt) {
		t.Fatalf("disabled provider = %#v", disabled)
	}

	listResponse := controlRequest(t, client, http.MethodGet, server.URL+"/api/control/providers", "", nil, http.StatusOK)
	var page pageView[providerView]
	decodeControlData(t, listResponse, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Slug != "fixture-provider" || page.Items[0].Status != "disabled" {
		t.Fatalf("provider page = %#v", page)
	}

	persisted := readPersistedProvider(t, pool, created.ID)
	if persisted.Slug != created.Slug || persisted.Name != disabled.Name || persisted.Kind != string(disabled.Kind) || persisted.BaseURL != disabled.BaseURL || persisted.Enabled || !persisted.UpdatedAt.Equal(disabled.UpdatedAt) {
		t.Fatalf("persisted provider = %#v", persisted)
	}

	createdAudit := readProviderAuditDetail(t, pool, created.ID, "provider.created")
	if createdAudit["slug"] != created.Slug || createdAudit["kind"] != "openai-compatible" {
		t.Fatalf("provider.created detail = %#v", createdAudit)
	}
	updatedAudit := readProviderAuditDetail(t, pool, created.ID, "provider.updated")
	if updatedAudit["name"] != updated.Name || updatedAudit["kind"] != string(updated.Kind) || updatedAudit["base_url"] != updated.BaseURL {
		t.Fatalf("provider.updated detail = %#v", updatedAudit)
	}
	var statusAuditCount, enabledAuditCount, disabledAuditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE detail ->> 'enabled' = 'true'), count(*) FILTER (WHERE detail ->> 'enabled' = 'false') FROM audit_events WHERE target_type = 'provider' AND target_id = $1 AND action = 'provider.status_changed'`, created.ID).Scan(&statusAuditCount, &enabledAuditCount, &disabledAuditCount); err != nil {
		t.Fatalf("read provider status audits: %v", err)
	}
	if statusAuditCount != 2 || enabledAuditCount != 1 || disabledAuditCount != 1 {
		t.Fatalf("provider status audits = total %d enabled %d disabled %d", statusAuditCount, enabledAuditCount, disabledAuditCount)
	}

	installProviderAuditFailure(t, pool)
	failedUpdate := controlRequest(t, client, http.MethodPut, server.URL+"/api/control/providers/"+created.ID, session.CSRFToken, map[string]any{
		"name": "Rollback Attempt", "kind": "agnes", "baseUrl": "https://198.18.0.4/v1", "expectedUpdatedAt": disabled.UpdatedAt,
	}, http.StatusInternalServerError)
	requireControlProblem(t, failedUpdate, "internal_error")

	afterAuditFailure := readPersistedProvider(t, pool, created.ID)
	if afterAuditFailure != persisted {
		t.Fatalf("provider changed despite audit failure: before %#v after %#v", persisted, afterAuditFailure)
	}
	var updateAuditCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_type = 'provider' AND target_id = $1 AND action = 'provider.updated'", created.ID).Scan(&updateAuditCount); err != nil {
		t.Fatalf("count provider update audits: %v", err)
	}
	if updateAuditCount != 1 {
		t.Fatalf("provider update audit count = %d, want 1", updateAuditCount)
	}
}

type persistedProvider struct {
	Slug      string
	Name      string
	Kind      string
	BaseURL   string
	Enabled   bool
	UpdatedAt time.Time
}

func readPersistedProvider(t *testing.T, pool *pgxpool.Pool, providerID string) persistedProvider {
	t.Helper()
	var result persistedProvider
	if err := pool.QueryRow(context.Background(), "SELECT slug, name, kind, base_url, enabled, updated_at FROM providers WHERE id = $1", providerID).Scan(&result.Slug, &result.Name, &result.Kind, &result.BaseURL, &result.Enabled, &result.UpdatedAt); err != nil {
		t.Fatalf("read persisted provider: %v", err)
	}
	return result
}

func readProviderAuditDetail(t *testing.T, pool *pgxpool.Pool, providerID, action string) map[string]any {
	t.Helper()
	var encoded []byte
	if err := pool.QueryRow(context.Background(), "SELECT detail FROM audit_events WHERE target_type = 'provider' AND target_id = $1 AND action = $2", providerID, action).Scan(&encoded); err != nil {
		t.Fatalf("read %s audit: %v", action, err)
	}
	var detail map[string]any
	if err := json.Unmarshal(encoded, &detail); err != nil {
		t.Fatalf("decode %s audit: %v", action, err)
	}
	return detail
}

func installProviderAuditFailure(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION reject_provider_update_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.action = 'provider.updated' AND NEW.detail ->> 'name' = 'Rollback Attempt' THEN
        RAISE EXCEPTION 'forced provider audit failure';
    END IF;
    RETURN NEW;
END;
$$`); err != nil {
		t.Fatalf("create audit failure function: %v", err)
	}
	if _, err := pool.Exec(ctx, "DROP TRIGGER IF EXISTS reject_provider_update_audit ON audit_events"); err != nil {
		t.Fatalf("drop prior audit failure trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE TRIGGER reject_provider_update_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_provider_update_audit()"); err != nil {
		t.Fatalf("create audit failure trigger: %v", err)
	}
}

func controlRequest(t *testing.T, client *http.Client, method, target, csrf string, body any, wantStatus int) *http.Response {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
	}
	request, err := http.NewRequest(method, target, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	if response.StatusCode != wantStatus {
		defer response.Body.Close()
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("%s %s status = %d, want %d: %s", method, target, response.StatusCode, wantStatus, payload)
	}
	return response
}

func decodeControlData(t *testing.T, response *http.Response, destination any) {
	t.Helper()
	defer response.Body.Close()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response envelope: %v", err)
	}
	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		t.Fatalf("decode response data: %v", err)
	}
}

func requireControlProblem(t *testing.T, response *http.Response, code string) {
	t.Helper()
	defer response.Body.Close()
	var envelope problemEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode problem response: %v", err)
	}
	if envelope.Error.Code != code {
		t.Fatalf("problem code = %q, want %q: %#v", envelope.Error.Code, code, envelope.Error)
	}
}
