package controlapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/operations"
)

type operationsService interface {
	Overview(context.Context, identity.Principal) (operations.Overview, error)
}

type OperationsAPI struct {
	service operationsService
	logger  *slog.Logger
}

type requestSummaryView struct {
	RequestCount      int64 `json:"requestCount"`
	CompletedCount    int64 `json:"completedCount"`
	FailedCount       int64 `json:"failedCount"`
	UncertainCount    int64 `json:"uncertainCount"`
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	FirstByteP95Ms    int64 `json:"firstByteP95Ms"`
	TotalLatencyP95Ms int64 `json:"totalLatencyP95Ms"`
}

type trendPointView struct {
	Bucket       time.Time `json:"bucket"`
	RequestCount int64     `json:"requestCount"`
	InputTokens  int64     `json:"inputTokens"`
	OutputTokens int64     `json:"outputTokens"`
}

type errorCountView struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

type stepView struct {
	ID       string `json:"id"`
	Complete bool   `json:"complete"`
}

type administratorResourcesView struct {
	ProviderCount          int64 `json:"providerCount"`
	EnabledProviderCount   int64 `json:"enabledProviderCount"`
	ModelCount             int64 `json:"modelCount"`
	CredentialCount        int64 `json:"credentialCount"`
	ActiveCredentialCount  int64 `json:"activeCredentialCount"`
	CoolingCredentialCount int64 `json:"coolingCredentialCount"`
	ActiveMemberCount      int64 `json:"activeMemberCount"`
	PendingMemberCount     int64 `json:"pendingMemberCount"`
	ActiveGatewayKeyCount  int64 `json:"activeGatewayKeyCount"`
	ActiveEntitlementCount int64 `json:"activeEntitlementCount"`
	HasActiveConfiguration bool  `json:"hasActiveConfiguration"`
	HasModelPrice          bool  `json:"hasModelPrice"`
}

type memberAccessView struct {
	ActiveGatewayKeyCount    int64      `json:"activeGatewayKeyCount"`
	ActiveEntitlementCount   int64      `json:"activeEntitlementCount"`
	RemainingTokens          int64      `json:"remainingTokens"`
	NearestEntitlementExpiry *time.Time `json:"nearestEntitlementExpiry,omitempty"`
}

func NewOperationsAPI(service operationsService, logger *slog.Logger) *OperationsAPI {
	if service == nil {
		panic("operations service is required")
	}
	return &OperationsAPI{service: service, logger: logger}
}

func (a *OperationsAPI) RegisterRoutes(router chi.Router) {
	router.Get("/overview", a.overview)
}

func (a *OperationsAPI) overview(w http.ResponseWriter, r *http.Request) {
	overview, err := a.service.Overview(r.Context(), principalFromContext(r.Context()))
	if err != nil {
		if errors.Is(err, operations.ErrForbidden) {
			writeProblem(w, r, problem{Status: http.StatusForbidden, Code: "forbidden", Message: "Forbidden.", Stage: "operations"})
			return
		}
		if a.logger != nil {
			a.logger.Error("operations overview failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		}
		writeProblem(w, r, problem{Status: http.StatusServiceUnavailable, Code: "overview_unavailable", Message: "Overview is unavailable.", Stage: "operations", Retryable: true})
		return
	}
	if overview.Administrator != nil {
		administrator := overview.Administrator
		writeData(w, http.StatusOK, struct {
			Scope     string                     `json:"scope"`
			Resources administratorResourcesView `json:"resources"`
			Requests  requestSummaryView         `json:"requests"`
			Trend     []trendPointView           `json:"trend"`
			Errors    []errorCountView           `json:"errors"`
			Steps     []stepView                 `json:"steps"`
		}{
			Scope: "administrator", Resources: presentAdministratorResources(administrator.Resources),
			Requests: presentRequestSummary(administrator.Requests), Trend: presentTrend(administrator.Trend),
			Errors: presentErrors(administrator.Errors), Steps: presentSteps(administrator.Steps),
		})
		return
	}
	member := overview.Member
	if member == nil {
		writeProblem(w, r, problem{Status: http.StatusInternalServerError, Code: "internal_invariant", Message: "Overview scope is unavailable.", Stage: "operations"})
		return
	}
	writeData(w, http.StatusOK, struct {
		Scope    string             `json:"scope"`
		Access   memberAccessView   `json:"access"`
		Requests requestSummaryView `json:"requests"`
		Trend    []trendPointView   `json:"trend"`
		Errors   []errorCountView   `json:"errors"`
		Steps    []stepView         `json:"steps"`
	}{
		Scope: "member", Access: presentMemberAccess(member.Access), Requests: presentRequestSummary(member.Requests),
		Trend: presentTrend(member.Trend), Errors: presentErrors(member.Errors), Steps: presentSteps(member.Steps),
	})
}

func presentAdministratorResources(resources operations.AdministratorResources) administratorResourcesView {
	return administratorResourcesView{
		ProviderCount: resources.ProviderCount, EnabledProviderCount: resources.EnabledProviderCount, ModelCount: resources.ModelCount,
		CredentialCount: resources.CredentialCount, ActiveCredentialCount: resources.ActiveCredentialCount, CoolingCredentialCount: resources.CoolingCredentialCount,
		ActiveMemberCount: resources.ActiveMemberCount, PendingMemberCount: resources.PendingMemberCount,
		ActiveGatewayKeyCount: resources.ActiveGatewayKeyCount, ActiveEntitlementCount: resources.ActiveEntitlementCount,
		HasActiveConfiguration: resources.HasActiveConfiguration, HasModelPrice: resources.HasModelPrice,
	}
}

func presentMemberAccess(access operations.MemberAccess) memberAccessView {
	return memberAccessView{
		ActiveGatewayKeyCount: access.ActiveGatewayKeyCount, ActiveEntitlementCount: access.ActiveEntitlementCount,
		RemainingTokens: access.RemainingTokens, NearestEntitlementExpiry: utcTimePointer(access.NearestEntitlementExpiry),
	}
}

func presentRequestSummary(summary operations.RequestSummary) requestSummaryView {
	return requestSummaryView{
		RequestCount: summary.RequestCount, CompletedCount: summary.CompletedCount, FailedCount: summary.FailedCount,
		UncertainCount: summary.UncertainCount, InputTokens: summary.InputTokens, OutputTokens: summary.OutputTokens,
		FirstByteP95Ms: summary.FirstByteP95Ms, TotalLatencyP95Ms: summary.TotalLatencyP95Ms,
	}
}

func presentTrend(points []operations.TrendPoint) []trendPointView {
	views := make([]trendPointView, 0, len(points))
	for _, point := range points {
		views = append(views, trendPointView{Bucket: point.Bucket.UTC(), RequestCount: point.RequestCount, InputTokens: point.InputTokens, OutputTokens: point.OutputTokens})
	}
	return views
}

func presentErrors(items []operations.ErrorCount) []errorCountView {
	views := make([]errorCountView, 0, len(items))
	for _, item := range items {
		views = append(views, errorCountView{Kind: item.Kind, Count: item.Count})
	}
	return views
}

func presentSteps(items []operations.Step) []stepView {
	views := make([]stepView, 0, len(items))
	for _, item := range items {
		views = append(views, stepView{ID: item.ID, Complete: item.Complete})
	}
	return views
}
