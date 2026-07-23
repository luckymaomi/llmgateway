package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/quota"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

type quotaControlServiceStub struct {
	createdInput  *quota.NewEntitlement
	items         []quota.Entitlement
	requestItems  []quota.RequestLog
	requestDetail quota.RequestLogDetail
	requestQuery  *quota.RequestLogQuery
	createError   error
}

func (s *quotaControlServiceStub) CreateEntitlement(_ context.Context, _ identity.Principal, input quota.NewEntitlement) (quota.Entitlement, error) {
	copy := input
	s.createdInput = &copy
	if s.createError != nil {
		return quota.Entitlement{}, s.createError
	}
	return s.items[0], nil
}

func (s *quotaControlServiceStub) ListEntitlements(_ context.Context, _ identity.Principal, query quota.EntitlementQuery) (quota.PageResult[quota.Entitlement], error) {
	if query.Page.Offset >= int32(len(s.items)) {
		return quota.PageResult[quota.Entitlement]{Items: []quota.Entitlement{}, Total: int64(len(s.items))}, nil
	}
	end := int(query.Page.Offset + query.Page.Size)
	if end > len(s.items) {
		end = len(s.items)
	}
	return quota.PageResult[quota.Entitlement]{Items: append([]quota.Entitlement(nil), s.items[query.Page.Offset:end]...), Total: int64(len(s.items))}, nil
}

func (s *quotaControlServiceStub) ListRequestLogs(_ context.Context, _ identity.Principal, query quota.RequestLogQuery) (quota.PageResult[quota.RequestLog], error) {
	copy := query
	s.requestQuery = &copy
	if query.Page.Offset >= int32(len(s.requestItems)) {
		return quota.PageResult[quota.RequestLog]{Items: []quota.RequestLog{}, Total: int64(len(s.requestItems))}, nil
	}
	end := int(query.Page.Offset + query.Page.Size)
	if end > len(s.requestItems) {
		end = len(s.requestItems)
	}
	return quota.PageResult[quota.RequestLog]{Items: append([]quota.RequestLog(nil), s.requestItems[query.Page.Offset:end]...), Total: int64(len(s.requestItems))}, nil
}

func TestQuotaControlListsAllRequestStatesAndRedactsMemberAttemptOwnership(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	requestID, ownerID, keyID, modelID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	errorKind := "upstream_outcome_uncertain"
	request := quota.RequestLog{
		RequestID: requestID, UserID: ownerID, UserName: "Request Member",
		GatewayKeyID: keyID, KeyPrefix: "llmg_abcd", ModelID: modelID, ModelAlias: "browser-chat",
		ResourceDomain: quota.ResourceFree, Status: quota.RequestUncertain, Stream: true,
		UsageSource: quota.UsageUnknown, ErrorKind: &errorKind, AcceptedAt: now.Add(-time.Minute),
		UpdatedAt: now, AttemptCount: 1,
	}
	service := &quotaControlServiceStub{
		requestItems: []quota.RequestLog{request},
		requestDetail: quota.RequestLogDetail{RequestLog: request, Attempts: []quota.RequestAttempt{{
			ID: uuid.New(), Sequence: 1, Status: "uncertain", ProviderName: "Provider One",
			CredentialName: "Primary upstream key", ErrorKind: &errorKind,
			UsageSource: quota.UsageUnknown, CreatedAt: now.Add(-time.Minute),
		}}},
	}
	api := NewQuotaAPI(service, quotaIdentityResolverStub{}, quotaModelResolverStub{}, nil)
	api.now = func() time.Time { return now }
	administrator := identity.Principal{UserID: uuid.New(), Role: identity.RoleAdministrator, Status: identity.StatusActive}
	administratorHandler := httpserver.RequestID(withQuotaPrincipal(administrator, api.Routes(quotaAdministratorMiddleware(administrator), passthroughMiddleware)))
	path := "/requests?status=uncertain&gatewayKeyId=" + keyID.String() + "&from=" + now.Add(-time.Hour).Format(time.RFC3339) + "&to=" + now.Format(time.RFC3339)
	listResponse := httptest.NewRecorder()
	administratorHandler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, path, nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("request log list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	page := decodeData[pageView[requestLogView]](t, listResponse)
	if len(page.Items) != 1 || page.Items[0].Status != quota.RequestUncertain || page.Items[0].ErrorKind == nil || *page.Items[0].ErrorKind != errorKind {
		t.Fatalf("request log page = %#v", page)
	}
	if service.requestQuery == nil || service.requestQuery.Status != quota.RequestUncertain || service.requestQuery.GatewayKeyID == nil || *service.requestQuery.GatewayKeyID != keyID {
		t.Fatalf("request log query = %#v", service.requestQuery)
	}

	detailResponse := httptest.NewRecorder()
	administratorHandler.ServeHTTP(detailResponse, httptest.NewRequest(http.MethodGet, "/requests/"+requestID.String(), nil))
	administratorDetail := decodeData[requestLogDetailView](t, detailResponse)
	if len(administratorDetail.Attempts) != 1 || administratorDetail.Attempts[0].ProviderName == nil || *administratorDetail.Attempts[0].ProviderName != "Provider One" {
		t.Fatalf("administrator request detail = %#v", administratorDetail)
	}

	member := identity.Principal{UserID: ownerID, DisplayName: "Request Member", Role: identity.RoleMember, Status: identity.StatusActive}
	memberHandler := httpserver.RequestID(withQuotaPrincipal(member, api.Routes(quotaAdministratorMiddleware(member), passthroughMiddleware)))
	memberResponse := httptest.NewRecorder()
	memberHandler.ServeHTTP(memberResponse, httptest.NewRequest(http.MethodGet, "/requests/"+requestID.String(), nil))
	memberDetail := decodeData[requestLogDetailView](t, memberResponse)
	if len(memberDetail.Attempts) != 1 || memberDetail.Attempts[0].ProviderName != nil || memberDetail.Attempts[0].CredentialName != nil {
		t.Fatalf("member request detail leaked upstream ownership = %#v", memberDetail)
	}
}

func (s *quotaControlServiceStub) GetRequestLog(_ context.Context, _ identity.Principal, _ uuid.UUID) (quota.RequestLogDetail, error) {
	return s.requestDetail, nil
}

func (s *quotaControlServiceStub) ListLedger(_ context.Context, _ identity.Principal, _ quota.LedgerFilter) (quota.PageResult[quota.LedgerEvent], error) {
	return quota.PageResult[quota.LedgerEvent]{Items: []quota.LedgerEvent{}}, nil
}

type quotaIdentityResolverStub struct {
	names map[uuid.UUID]string
}

func (s quotaIdentityResolverStub) UserDisplayNames(_ context.Context, _ identity.Principal, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	result := make(map[uuid.UUID]string, len(userIDs))
	for _, userID := range userIDs {
		if name := s.names[userID]; name != "" {
			result[userID] = name
		}
	}
	return result, nil
}

type quotaModelResolverStub struct {
	models []registry.Model
}

func (s quotaModelResolverStub) ListModels(context.Context, identity.Principal) ([]registry.Model, error) {
	return append([]registry.Model(nil), s.models...), nil
}

func TestQuotaControlCreatesAndListsAnIdempotentStructuredEntitlement(t *testing.T) {
	actorID, ownerID, providerID, modelID, entitlementID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	service := &quotaControlServiceStub{items: []quota.Entitlement{{
		ID: entitlementID, UserID: ownerID, Plan: quota.PlanToken, ResourceDomain: quota.ResourceFree,
		ModelID: &modelID, GrantedTokens: 50_000, BalanceTokens: 50_000,
		StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), ConcurrencyLimit: 2,
		OwnerName: "Quota Member", ModelAlias: stringPointer("free-chat"),
	}}}
	api := NewQuotaAPI(
		service,
		quotaIdentityResolverStub{names: map[uuid.UUID]string{ownerID: "Quota Member"}},
		quotaModelResolverStub{models: []registry.Model{{ID: modelID, ProviderID: providerID, PublicName: "free-chat", ResourceDomain: registry.ResourceFree}}},
		nil,
	)
	api.now = func() time.Time { return now }
	principal := identity.Principal{UserID: actorID, Role: identity.RoleAdministrator, Status: identity.StatusActive}
	handler := httpserver.RequestID(withQuotaPrincipal(principal, api.Routes(quotaAdministratorMiddleware(principal), passthroughMiddleware)))

	idempotencyKey := uuid.New()
	body := map[string]any{
		"ownerId": ownerID.String(), "planKind": "token", "resourceDomain": "free",
		"modelId": modelID.String(), "grantedTokens": 50_000, "concurrencyLimit": 2,
		"startsAt": now.Add(-time.Hour), "expiresAt": now.Add(30 * 24 * time.Hour),
		"reason": "team allocation",
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/entitlements", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey.String())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create entitlement status = %d body = %s", response.Code, response.Body.String())
	}
	if service.createdInput == nil || service.createdInput.IdempotencyKey != idempotencyKey || service.createdInput.RequestID == "" || service.createdInput.UserID != ownerID || service.createdInput.ModelID == nil || *service.createdInput.ModelID != modelID || service.createdInput.Note != "team allocation" {
		t.Fatalf("created quota input = %#v", service.createdInput)
	}
	created := decodeData[entitlementView](t, response)
	if created.OwnerName != "Quota Member" || created.ModelAlias == nil || *created.ModelAlias != "free-chat" || created.BalanceTokens != 50_000 || created.Status != "active" {
		t.Fatalf("created entitlement view = %#v", created)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/entitlements?page=1&pageSize=20", nil)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list entitlement status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	page := decodeData[pageView[entitlementView]](t, listResponse)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != entitlementID.String() {
		t.Fatalf("entitlement page = %#v", page)
	}
}

func TestQuotaControlRejectsMissingIdempotencyAndNonAdministratorAccess(t *testing.T) {
	ownerID := uuid.New()
	now := time.Now().UTC()
	service := &quotaControlServiceStub{items: []quota.Entitlement{{
		ID: uuid.New(), UserID: ownerID, Plan: quota.PlanToken, ResourceDomain: quota.ResourceFree,
		GrantedTokens: 1, BalanceTokens: 1, StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), ConcurrencyLimit: 1,
		OwnerName: "Member",
	}}}
	api := NewQuotaAPI(service, quotaIdentityResolverStub{names: map[uuid.UUID]string{ownerID: "Member"}}, quotaModelResolverStub{}, nil)
	member := identity.Principal{UserID: ownerID, Role: identity.RoleMember, Status: identity.StatusActive}
	handler := httpserver.RequestID(withQuotaPrincipal(member, api.Routes(quotaAdministratorMiddleware(member), passthroughMiddleware)))

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/entitlements", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("member entitlement list status = %d body = %s", listResponse.Code, listResponse.Body.String())
	}
	createAsMember := httptest.NewRecorder()
	handler.ServeHTTP(createAsMember, httptest.NewRequest(http.MethodPost, "/entitlements", bytes.NewBufferString("{}")))
	if createAsMember.Code != http.StatusForbidden {
		t.Fatalf("member entitlement create status = %d, want 403", createAsMember.Code)
	}

	administrator := identity.Principal{UserID: uuid.New(), Role: identity.RoleAdministrator, Status: identity.StatusActive}
	administratorHandler := httpserver.RequestID(withQuotaPrincipal(administrator, api.Routes(quotaAdministratorMiddleware(administrator), passthroughMiddleware)))
	createResponse := httptest.NewRecorder()
	administratorHandler.ServeHTTP(createResponse, httptest.NewRequest(http.MethodPost, "/entitlements", bytes.NewBufferString("{}")))
	if createResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key status = %d, want 400", createResponse.Code)
	}
	if service.createdInput != nil {
		t.Fatal("quota request without an Idempotency-Key reached the service")
	}
}

func quotaAdministratorMiddleware(principal identity.Principal) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !principal.CanManageUsers() {
				writeProblem(w, r, problem{Status: http.StatusForbidden, Code: "forbidden", Message: "Forbidden.", Retryable: false})
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
		})
	}
}

func passthroughMiddleware(next http.Handler) http.Handler {
	return next
}

func withQuotaPrincipal(principal identity.Principal, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}

func stringPointer(value string) *string {
	return &value
}
