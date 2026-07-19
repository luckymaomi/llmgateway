package controlapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/quota"
)

// QuotaAPI assumes its routes are mounted inside API's authenticated route group.
// The supplied mutation middleware must enforce the control-plane CSRF contract.
type QuotaAPI struct {
	service *quota.Service
	logger  *slog.Logger
}

func NewQuotaAPI(service *quota.Service, logger *slog.Logger) *QuotaAPI {
	return &QuotaAPI{service: service, logger: logger}
}

func (a *QuotaAPI) Routes(mutationMiddleware func(http.Handler) http.Handler) http.Handler {
	if mutationMiddleware == nil {
		panic("quota mutation middleware is required")
	}
	router := chi.NewRouter()
	router.Get("/model-authorizations/{userID}", a.listModelAuthorizations)
	router.Get("/entitlements", a.listEntitlements)
	router.Get("/ledger/entries", a.listLedger)
	router.Group(func(mutating chi.Router) {
		mutating.Use(mutationMiddleware)
		mutating.Put("/model-authorizations/{userID}/{modelID}", a.authorizeModel)
		mutating.Delete("/model-authorizations/{userID}/{modelID}", a.revokeModel)
		mutating.Post("/entitlements", a.createEntitlement)
	})
	return router
}

func (a *QuotaAPI) authorizeModel(w http.ResponseWriter, r *http.Request) {
	userID, modelID, err := authorizationIDs(r)
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if err := a.service.AuthorizeModel(r.Context(), principalFromContext(r.Context()), userID, modelID); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *QuotaAPI) revokeModel(w http.ResponseWriter, r *http.Request) {
	userID, modelID, err := authorizationIDs(r)
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if err := a.service.RevokeModel(r.Context(), principalFromContext(r.Context()), userID, modelID); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *QuotaAPI) listModelAuthorizations(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	items, err := a.service.ListModelAuthorizations(r.Context(), principalFromContext(r.Context()), userID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *QuotaAPI) createEntitlement(w http.ResponseWriter, r *http.Request) {
	idempotencyKey, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		UserID           uuid.UUID            `json:"user_id"`
		Plan             quota.Plan           `json:"plan"`
		ResourceDomain   quota.ResourceDomain `json:"resource_domain"`
		ModelID          *uuid.UUID           `json:"model_id"`
		GrantedTokens    int64                `json:"granted_tokens"`
		StartsAt         time.Time            `json:"starts_at"`
		ExpiresAt        time.Time            `json:"expires_at"`
		ConcurrencyLimit int32                `json:"concurrency_limit"`
		RPMLimit         *int32               `json:"rpm_limit"`
		TPMLimit         *int64               `json:"tpm_limit"`
		Note             string               `json:"note"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	created, err := a.service.CreateEntitlement(r.Context(), principalFromContext(r.Context()), quota.NewEntitlement{
		IdempotencyKey: idempotencyKey, UserID: input.UserID, Plan: input.Plan, ResourceDomain: input.ResourceDomain, ModelID: input.ModelID,
		GrantedTokens: input.GrantedTokens, StartsAt: input.StartsAt, ExpiresAt: input.ExpiresAt,
		ConcurrencyLimit: input.ConcurrencyLimit, RPMLimit: input.RPMLimit, TPMLimit: input.TPMLimit, Note: input.Note,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, created)
}

func (a *QuotaAPI) listEntitlements(w http.ResponseWriter, r *http.Request) {
	userID, err := optionalUUID(r.URL.Query().Get("user_id"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	items, err := a.service.ListEntitlements(r.Context(), principalFromContext(r.Context()), userID, quotaPage(r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *QuotaAPI) listLedger(w http.ResponseWriter, r *http.Request) {
	userID, userErr := optionalUUID(r.URL.Query().Get("user_id"))
	entitlementID, entitlementErr := optionalUUID(r.URL.Query().Get("entitlement_id"))
	if userErr != nil || entitlementErr != nil {
		writeDecodeError(w, r, quota.ErrInvalidInput)
		return
	}
	items, err := a.service.ListLedger(r.Context(), principalFromContext(r.Context()), quota.LedgerFilter{
		UserID: userID, EntitlementID: entitlementID, Page: quotaPage(r),
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *QuotaAPI) writeError(w http.ResponseWriter, r *http.Request, err error) {
	problem := httpserver.Problem{Type: "about:blank", RequestID: httpserver.RequestIDFromContext(r.Context())}
	switch {
	case errors.Is(err, quota.ErrInvalidInput):
		problem.Title, problem.Status, problem.Code = "Invalid quota request", http.StatusBadRequest, "invalid_request"
	case errors.Is(err, quota.ErrForbidden):
		problem.Title, problem.Status, problem.Code = "Forbidden", http.StatusForbidden, "forbidden"
	case errors.Is(err, quota.ErrNotFound):
		problem.Title, problem.Status, problem.Code = "Not found", http.StatusNotFound, "not_found"
	case errors.Is(err, quota.ErrResourceDomainMismatch):
		problem.Title, problem.Status, problem.Code = "Resource domain mismatch", http.StatusConflict, "resource_domain_mismatch"
	case errors.Is(err, quota.ErrConflict), errors.Is(err, quota.ErrTerminalConflict):
		problem.Title, problem.Status, problem.Code = "Quota state changed", http.StatusConflict, "quota_conflict"
	default:
		a.logger.Error("quota operation failed", "request_id", problem.RequestID, "error", err)
		problem.Title, problem.Status, problem.Code = "Internal server error", http.StatusInternalServerError, "internal_error"
	}
	httpserver.WriteProblem(w, problem)
}

func authorizationIDs(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	userID, userErr := uuid.Parse(chi.URLParam(r, "userID"))
	modelID, modelErr := uuid.Parse(chi.URLParam(r, "modelID"))
	if userErr != nil {
		return uuid.Nil, uuid.Nil, userErr
	}
	return userID, modelID, modelErr
}

func optionalUUID(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func quotaPage(r *http.Request) quota.Page {
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 32)
	size, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32)
	return quota.Page{Offset: int32(offset), Size: int32(size)}
}
