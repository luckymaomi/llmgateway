package controlapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type testDataEnvelope[T any] struct {
	Data T `json:"data"`
}

type controlFixture struct {
	handler       http.Handler
	identity      *identityStub
	registry      *registryStub
	configuration *configurationStub
	adminID       uuid.UUID
	memberID      uuid.UUID
	activeID      uuid.UUID
	draftID       uuid.UUID
	activeModelID uuid.UUID
	now           time.Time
}

func newControlFixture(t *testing.T) controlFixture {
	t.Helper()
	now := time.Date(2026, time.July, 19, 8, 0, 0, 0, time.UTC)
	adminID := uuid.New()
	memberID := uuid.New()
	activeID := uuid.New()
	draftID := uuid.New()
	activeProviderID := uuid.New()
	activeModelID := uuid.New()
	activeCredentialID := uuid.New()
	principal := identity.Principal{
		SessionID:   uuid.New(),
		UserID:      adminID,
		Email:       "admin@example.test",
		DisplayName: "Admin",
		Role:        identity.RoleAdministrator,
		Status:      identity.StatusActive,
		ExpiresAt:   now.Add(8 * time.Hour),
	}
	identityService := &identityStub{
		principal: principal,
		credentials: identity.SessionCredentials{
			Principal: principal,
			Token:     "session-test",
			CSRFToken: "csrf-test-token",
		},
		registered: identity.User{ID: uuid.New(), Role: identity.RoleMember, Status: identity.StatusPending},
		users: []identity.User{
			{ID: adminID, Email: principal.Email, DisplayName: principal.DisplayName, Role: principal.Role, Status: identity.StatusActive, CreatedAt: now},
			{ID: memberID, Email: "member@example.test", DisplayName: "Member", Role: identity.RoleMember, Status: identity.StatusPending, CreatedAt: now.Add(time.Minute)},
		},
		keys: make(map[uuid.UUID][]identity.GatewayKey),
	}
	configurationService := &configurationStub{
		active: configuration.Active{
			Revision:  configuration.Revision{ID: activeID, Revision: 4, Checksum: "active-checksum", Catalog: configuration.CatalogSummary{ProviderCount: 1, ModelCount: 1, CredentialCount: 1, RouteCount: 1}, CreatedBy: adminID, CreatedAt: now},
			Version:   7,
			UpdatedAt: now,
		},
		revisions: []configuration.Revision{
			{ID: activeID, Revision: 4, Checksum: "active-checksum", Catalog: configuration.CatalogSummary{ProviderCount: 1, ModelCount: 1, CredentialCount: 1, RouteCount: 1}, CreatedBy: adminID, CreatedAt: now},
			{ID: draftID, Revision: 5, Checksum: "draft-checksum", Catalog: configuration.CatalogSummary{ProviderCount: 1, ModelCount: 1, CredentialCount: 1, RouteCount: 1}, CreatedBy: adminID, CreatedAt: now.Add(time.Minute)},
		},
		catalog: configuration.Catalog{
			Providers:   []configuration.CatalogProvider{{ID: activeProviderID, Slug: "published", Name: "Published Provider", Kind: "openai_compatible", BaseURL: "https://published.example.test/v1"}},
			Models:      []configuration.CatalogModel{{ID: activeModelID, ProviderID: activeProviderID, PublicName: "fast", UpstreamName: "fast-upstream", DisplayName: "Fast", ResourceDomain: "professional", CreatedAt: now}},
			Credentials: []configuration.CatalogCredential{{ID: activeCredentialID, ProviderID: activeProviderID, ResourceDomain: "professional"}},
			Routes:      []configuration.CatalogRoute{{ModelID: activeModelID, CredentialID: activeCredentialID, Priority: 100, Weight: 100}},
		},
	}
	registryService := &registryStub{}
	api := New(identityService, registryService, configurationService, nil, config.Security{}, nil)
	router := chi.NewRouter()
	router.Use(httpserver.RequestID)
	router.Mount("/api", api.Routes())
	return controlFixture{
		handler:       router,
		identity:      identityService,
		registry:      registryService,
		configuration: configurationService,
		adminID:       adminID,
		memberID:      memberID,
		activeID:      activeID,
		draftID:       draftID,
		activeModelID: activeModelID,
		now:           now,
	}
}

func request(t *testing.T, handler http.Handler, method, path string, body any, authenticated, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	idempotencyKey := ""
	if method != http.MethodGet {
		idempotencyKey = uuid.NewString()
	}
	return requestWithIdempotencyKey(t, handler, method, path, body, authenticated, csrf, idempotencyKey)
}

func requestWithIdempotencyKey(t *testing.T, handler http.Handler, method, path string, body any, authenticated, csrf bool, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-test"})
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-test-token"})
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", "csrf-test-token")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeData[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	var envelope testDataEnvelope[T]
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, recorder.Body.String())
	}
	return envelope.Data
}

func requireStatus(t *testing.T, recorder *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if recorder.Code != expected {
		t.Fatalf("status = %d, want %d\nbody: %s", recorder.Code, expected, recorder.Body.String())
	}
}
