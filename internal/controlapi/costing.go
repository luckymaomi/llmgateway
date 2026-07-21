package controlapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/costing"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type costingService interface {
	CreatePriceVersion(context.Context, identity.Principal, costing.NewPriceVersion, costing.MutationRequest) (costing.PriceVersion, error)
	ListPriceVersions(context.Context, identity.Principal, *uuid.UUID, costing.Page) ([]costing.PriceVersion, error)
	ListSummaries(context.Context, identity.Principal, costing.Page) ([]costing.Summary, error)
}

type CostingAPI struct {
	service costingService
	logger  *slog.Logger
}

func NewCostingAPI(service costingService, logger *slog.Logger) *CostingAPI {
	return &CostingAPI{service: service, logger: logger}
}

func (a *CostingAPI) RegisterRoutes(router chi.Router, authorizationMiddleware, mutationMiddleware func(http.Handler) http.Handler) {
	router.With(authorizationMiddleware).Get("/model-prices", a.listPriceVersions)
	router.With(authorizationMiddleware, mutationMiddleware).Post("/model-prices", a.createPriceVersion)
	router.With(authorizationMiddleware).Get("/costs", a.listSummaries)
}

type priceVersionView struct {
	ID                          string    `json:"id"`
	ModelID                     string    `json:"modelId"`
	ModelAlias                  string    `json:"modelAlias"`
	Currency                    string    `json:"currency"`
	InputPricePerMillionTokens  string    `json:"inputPricePerMillionTokens"`
	OutputPricePerMillionTokens string    `json:"outputPricePerMillionTokens"`
	EffectiveAt                 time.Time `json:"effectiveAt"`
	CreatedAt                   time.Time `json:"createdAt"`
}

type costSummaryView struct {
	UserID          string `json:"userId"`
	UserName        string `json:"userName"`
	EntitlementID   string `json:"entitlementId"`
	Plan            string `json:"plan"`
	ModelID         string `json:"modelId"`
	ModelAlias      string `json:"modelAlias"`
	ProviderID      string `json:"providerId"`
	ProviderName    string `json:"providerName"`
	ResourceDomain  string `json:"resourceDomain"`
	Currency        string `json:"currency"`
	RequestCount    int64  `json:"requestCount"`
	InputTokens     int64  `json:"inputTokens"`
	OutputTokens    int64  `json:"outputTokens"`
	InputCostNanos  string `json:"inputCostNanos"`
	OutputCostNanos string `json:"outputCostNanos"`
	TotalCostNanos  string `json:"totalCostNanos"`
}

func (a *CostingAPI) createPriceVersion(w http.ResponseWriter, r *http.Request) {
	idempotencyKey, err := uuid.Parse(strings.TrimSpace(r.Header.Get("Idempotency-Key")))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		ModelID                     uuid.UUID `json:"modelId"`
		Currency                    string    `json:"currency"`
		InputPricePerMillionTokens  string    `json:"inputPricePerMillionTokens"`
		OutputPricePerMillionTokens string    `json:"outputPricePerMillionTokens"`
		EffectiveAt                 time.Time `json:"effectiveAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	created, err := a.service.CreatePriceVersion(r.Context(), principalFromContext(r.Context()), costing.NewPriceVersion{
		ModelID: input.ModelID, Currency: input.Currency, InputPricePerMillionTokens: input.InputPricePerMillionTokens,
		OutputPricePerMillionTokens: input.OutputPricePerMillionTokens, EffectiveAt: input.EffectiveAt,
	}, costing.MutationRequest{IdempotencyKey: idempotencyKey, RequestID: httpserver.RequestIDFromContext(r.Context())})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	status := http.StatusCreated
	if created.Replayed {
		status = http.StatusOK
	}
	writeData(w, status, presentPriceVersion(created))
}

func (a *CostingAPI) listPriceVersions(w http.ResponseWriter, r *http.Request) {
	var modelID *uuid.UUID
	if value := strings.TrimSpace(r.URL.Query().Get("modelId")); value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil {
			writeDecodeError(w, r, err)
			return
		}
		modelID = &parsed
	}
	principal := principalFromContext(r.Context())
	items := make([]priceVersionView, 0)
	for offset := int32(0); ; offset += 200 {
		versions, err := a.service.ListPriceVersions(r.Context(), principal, modelID, costing.Page{Offset: offset, Size: 200})
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		for _, version := range versions {
			items = append(items, presentPriceVersion(version))
		}
		if len(versions) < 200 {
			break
		}
	}
	writeData(w, http.StatusOK, paginate(items, parseListQuery(r)))
}

func (a *CostingAPI) listSummaries(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	items := make([]costSummaryView, 0)
	for offset := int32(0); ; offset += 200 {
		summaries, err := a.service.ListSummaries(r.Context(), principal, costing.Page{Offset: offset, Size: 200})
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		for _, summary := range summaries {
			items = append(items, presentCostSummary(summary))
		}
		if len(summaries) < 200 {
			break
		}
	}
	query := parseListQuery(r)
	filtered := make([]costSummaryView, 0, len(items))
	for _, item := range items {
		if query.ResourceDomain != "" && item.ResourceDomain != query.ResourceDomain || !containsFold(item.UserName+" "+item.ModelAlias+" "+item.ProviderName+" "+item.Currency, query.Search) {
			continue
		}
		filtered = append(filtered, item)
	}
	writeData(w, http.StatusOK, paginate(filtered, query))
}

func presentPriceVersion(value costing.PriceVersion) priceVersionView {
	return priceVersionView{ID: value.ID.String(), ModelID: value.ModelID.String(), ModelAlias: value.ModelAlias, Currency: value.Currency,
		InputPricePerMillionTokens: costing.FormatRate(value.InputRateNanosPerMillion), OutputPricePerMillionTokens: costing.FormatRate(value.OutputRateNanosPerMillion),
		EffectiveAt: value.EffectiveAt.UTC(), CreatedAt: value.CreatedAt.UTC()}
}

func presentCostSummary(value costing.Summary) costSummaryView {
	return costSummaryView{UserID: value.UserID.String(), UserName: value.UserName, EntitlementID: value.EntitlementID.String(), Plan: value.Plan,
		ModelID: value.ModelID.String(), ModelAlias: value.ModelAlias, ProviderID: value.ProviderID.String(), ProviderName: value.ProviderName,
		ResourceDomain: value.ResourceDomain, Currency: value.Currency, RequestCount: value.RequestCount,
		InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, InputCostNanos: strconv.FormatInt(value.InputCostNanos, 10),
		OutputCostNanos: strconv.FormatInt(value.OutputCostNanos, 10), TotalCostNanos: strconv.FormatInt(value.TotalCostNanos, 10)}
}

func (a *CostingAPI) writeError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Cost operation failed.", Retryable: true, Stage: "costing"}
	switch {
	case errors.Is(err, costing.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_request", "Cost input is invalid.", false
	case errors.Is(err, costing.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot read or manage cost facts.", false
	case errors.Is(err, costing.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "The model or price version was not found.", false
	case errors.Is(err, costing.ErrConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for different price input.", false
	case errors.Is(err, costing.ErrOutcomeUnknown):
		value.Status, value.Code, value.Message = http.StatusServiceUnavailable, "operation_outcome_unknown", "The price operation may have committed. Retry with the same Idempotency-Key."
	default:
		if a.logger != nil {
			a.logger.Error("costing operation failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		}
	}
	writeProblem(w, r, value)
}
