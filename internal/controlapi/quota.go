package controlapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/quota"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

type quotaService interface {
	CreateEntitlement(context.Context, identity.Principal, quota.NewEntitlement) (quota.Entitlement, error)
	ListEntitlements(context.Context, identity.Principal, quota.EntitlementQuery) (quota.PageResult[quota.Entitlement], error)
	ListLedger(context.Context, identity.Principal, quota.LedgerFilter) (quota.PageResult[quota.LedgerEvent], error)
	ListUsage(context.Context, identity.Principal, quota.UsageQuery) (quota.PageResult[quota.UsageRecord], error)
}

type quotaIdentityResolver interface {
	UserDisplayNames(context.Context, identity.Principal, []uuid.UUID) (map[uuid.UUID]string, error)
}

type quotaModelResolver interface {
	ListModels(context.Context, identity.Principal) ([]registry.Model, error)
}

type QuotaAPI struct {
	service  quotaService
	identity quotaIdentityResolver
	registry quotaModelResolver
	logger   *slog.Logger
	now      func() time.Time
}

func NewQuotaAPI(service quotaService, identityResolver quotaIdentityResolver, modelResolver quotaModelResolver, logger *slog.Logger) *QuotaAPI {
	return &QuotaAPI{service: service, identity: identityResolver, registry: modelResolver, logger: logger, now: time.Now}
}

func (a *QuotaAPI) RegisterRoutes(router chi.Router, authorizationMiddleware, mutationMiddleware func(http.Handler) http.Handler) {
	if authorizationMiddleware == nil || mutationMiddleware == nil {
		panic("quota authorization and mutation middleware are required")
	}
	router.With(authorizationMiddleware).Get("/entitlements", a.listEntitlements)
	router.With(authorizationMiddleware, mutationMiddleware).Post("/entitlements", a.createEntitlement)
	router.With(authorizationMiddleware).Get("/ledger/entries", a.listLedgerEntries)
	router.Get("/usage", a.listUsage)
}

func (a *QuotaAPI) listUsage(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	query := parseListQuery(r)
	page, ok := quotaPage(query)
	if !ok {
		a.writeError(w, r, quota.ErrInvalidInput)
		return
	}
	result, err := a.service.ListUsage(r.Context(), principal, quota.UsageQuery{
		Search: query.Search, ResourceDomain: quota.ResourceDomain(query.ResourceDomain), Page: page,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	views, err := a.presentUsage(r.Context(), principal, result.Items)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pageView[usageView]{Items: views, Page: query.Page, PageSize: query.PageSize, Total: int(result.Total)})
}

func (a *QuotaAPI) Routes(authorizationMiddleware, mutationMiddleware func(http.Handler) http.Handler) http.Handler {
	router := chi.NewRouter()
	a.RegisterRoutes(router, authorizationMiddleware, mutationMiddleware)
	return router
}

func (a *QuotaAPI) createEntitlement(w http.ResponseWriter, r *http.Request) {
	idempotencyKey, err := uuid.Parse(strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input entitlementInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	principal := principalFromContext(r.Context())
	ownerName, err := a.resolveOwnerName(r.Context(), principal, input.OwnerID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	modelAlias, err := a.resolveModelAlias(r.Context(), principal, input.ModelID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	created, err := a.service.CreateEntitlement(r.Context(), principal, quota.NewEntitlement{
		IdempotencyKey:   idempotencyKey,
		RequestID:        httpserver.RequestIDFromContext(r.Context()),
		UserID:           input.OwnerID,
		Plan:             input.Plan,
		ResourceDomain:   input.ResourceDomain,
		ModelID:          input.ModelID,
		GrantedTokens:    input.GrantedTokens,
		StartsAt:         input.StartsAt,
		ExpiresAt:        input.ExpiresAt,
		ConcurrencyLimit: input.ConcurrencyLimit,
		RPMLimit:         input.RPMLimit,
		TPMLimit:         input.TPMLimit,
		Note:             input.Reason,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, presentEntitlement(created, ownerName, modelAlias, a.now().UTC()))
}

func (a *QuotaAPI) listEntitlements(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	query := parseListQuery(r)
	page, ok := quotaPage(query)
	if !ok {
		a.writeError(w, r, quota.ErrInvalidInput)
		return
	}
	result, err := a.service.ListEntitlements(r.Context(), principal, quota.EntitlementQuery{
		Search: query.Search, Status: query.Status, ResourceDomain: quota.ResourceDomain(query.ResourceDomain), Page: page,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	views, err := a.presentEntitlements(r.Context(), principal, result.Items)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pageView[entitlementView]{Items: views, Page: query.Page, PageSize: query.PageSize, Total: int(result.Total)})
}

func quotaPage(query listQuery) (quota.Page, bool) {
	offset := int64(query.Page-1) * int64(query.PageSize)
	if offset > int64(^uint32(0)>>1) {
		return quota.Page{}, false
	}
	return quota.Page{Offset: int32(offset), Size: int32(query.PageSize)}, true
}

func (a *QuotaAPI) writeError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Quota operation failed.", Retryable: true, Stage: "quota"}
	switch {
	case errors.Is(err, quota.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_request", "Quota input is invalid.", false
	case errors.Is(err, quota.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot manage quota.", false
	case errors.Is(err, quota.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "Quota input references a missing record.", false
	case errors.Is(err, quota.ErrResourceDomainMismatch):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "resource_domain_mismatch", "The model and entitlement resource domains differ.", false
	case errors.Is(err, quota.ErrConflict), errors.Is(err, quota.ErrTerminalConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for different quota input.", false
	case errors.Is(err, quota.ErrOutcomeUnknown):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusServiceUnavailable, "operation_outcome_unknown", "The quota operation may have committed. Retry with the same Idempotency-Key.", true
	default:
		if a.logger != nil {
			a.logger.Error("quota operation failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		}
	}
	writeProblem(w, r, value)
}
