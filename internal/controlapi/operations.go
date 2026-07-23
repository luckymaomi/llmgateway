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

type administratorResourcesView struct {
	ResourcePoolCount              int64 `json:"resourcePoolCount"`
	ActiveResourcePoolCount        int64 `json:"activeResourcePoolCount"`
	ConnectedProviderCount         int64 `json:"connectedProviderCount"`
	ModelCount                     int64 `json:"modelCount"`
	CredentialCount                int64 `json:"credentialCount"`
	ActiveCredentialCount          int64 `json:"activeCredentialCount"`
	CoolingCredentialCount         int64 `json:"coolingCredentialCount"`
	SuccessfulCredentialProbeCount int64 `json:"successfulCredentialProbeCount"`
	ActiveMemberCount              int64 `json:"activeMemberCount"`
	ActiveGatewayKeyCount          int64 `json:"activeApiKeyCount"`
	ActiveServicePlanCount         int64 `json:"activeServicePlanCount"`
	ActiveSubscriptionCount        int64 `json:"activeSubscriptionCount"`
	HasActiveUpstream              bool  `json:"hasActiveUpstream"`
	HasModelPrice                  bool  `json:"hasModelPrice"`
	HasCompletedRequest            bool  `json:"hasCompletedRequest"`
}

type memberAccessView struct {
	ActiveGatewayKeyCount     int64      `json:"activeApiKeyCount"`
	ActiveSubscriptionCount   int64      `json:"activeSubscriptionCount"`
	RemainingTokens           int64      `json:"remainingTokens"`
	NearestSubscriptionExpiry *time.Time `json:"nearestSubscriptionExpiry,omitempty"`
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
		}{
			Scope: "administrator", Resources: presentAdministratorResources(administrator.Resources),
			Requests: presentRequestSummary(administrator.Requests), Trend: presentTrend(administrator.Trend),
			Errors: presentErrors(administrator.Errors),
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
	}{
		Scope: "member", Access: presentMemberAccess(member.Access), Requests: presentRequestSummary(member.Requests),
		Trend: presentTrend(member.Trend), Errors: presentErrors(member.Errors),
	})
}

func presentAdministratorResources(resources operations.AdministratorResources) administratorResourcesView {
	return administratorResourcesView{
		ResourcePoolCount: resources.ResourcePoolCount, ActiveResourcePoolCount: resources.ActiveResourcePoolCount,
		ConnectedProviderCount: resources.ConnectedProviderCount, ModelCount: resources.ModelCount,
		CredentialCount: resources.CredentialCount, ActiveCredentialCount: resources.ActiveCredentialCount, CoolingCredentialCount: resources.CoolingCredentialCount,
		SuccessfulCredentialProbeCount: resources.SuccessfulCredentialProbeCount,
		ActiveMemberCount:              resources.ActiveMemberCount,
		ActiveGatewayKeyCount:          resources.ActiveGatewayKeyCount, ActiveServicePlanCount: resources.ActiveServicePlanCount,
		ActiveSubscriptionCount: resources.ActiveSubscriptionCount, HasActiveUpstream: resources.HasActiveUpstream, HasModelPrice: resources.HasModelPrice,
		HasCompletedRequest: resources.HasCompletedRequest,
	}
}

func presentMemberAccess(access operations.MemberAccess) memberAccessView {
	return memberAccessView{
		ActiveGatewayKeyCount: access.ActiveGatewayKeyCount, ActiveSubscriptionCount: access.ActiveSubscriptionCount,
		RemainingTokens: access.RemainingTokens, NearestSubscriptionExpiry: utcTimePointer(access.NearestSubscriptionExpiry),
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
