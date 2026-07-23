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
)

type quotaService interface {
	ListLedger(context.Context, identity.Principal, quota.LedgerFilter) (quota.PageResult[quota.LedgerEvent], error)
	ListRequestLogs(context.Context, identity.Principal, quota.RequestLogQuery) (quota.PageResult[quota.RequestLog], error)
	GetRequestLog(context.Context, identity.Principal, uuid.UUID) (quota.RequestLogDetail, error)
}

type QuotaAPI struct {
	service quotaService
	logger  *slog.Logger
	now     func() time.Time
}

func NewQuotaAPI(service quotaService, logger *slog.Logger) *QuotaAPI {
	return &QuotaAPI{service: service, logger: logger, now: time.Now}
}

func (a *QuotaAPI) RegisterRoutes(router chi.Router, _, _ func(http.Handler) http.Handler) {
	router.Get("/ledger/entries", a.listLedgerEntries)
	router.Get("/requests", a.listRequestLogs)
	router.Get("/requests/{requestID}", a.getRequestLog)
}

func (a *QuotaAPI) listLedgerEntries(w http.ResponseWriter, r *http.Request) {
	query := parseListQuery(r)
	userID, err := optionalUUID(query.UserID)
	if err != nil {
		a.writeError(w, r, quota.ErrInvalidInput)
		return
	}
	subscriptionID, err := optionalUUID(query.SubscriptionID)
	if err != nil {
		a.writeError(w, r, quota.ErrInvalidInput)
		return
	}
	result, err := a.service.ListLedger(r.Context(), principalFromContext(r.Context()), quota.LedgerFilter{UserID: userID, SubscriptionID: subscriptionID, Search: query.Search, Page: quota.Page{Offset: query.offset(), Size: int32(query.PageSize)}})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	views, err := presentLedgerEntries(principalFromContext(r.Context()), result.Items)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pageView[ledgerEntryView]{Items: views, Page: query.Page, PageSize: query.PageSize, Total: result.Total})
}

func (a *QuotaAPI) listRequestLogs(w http.ResponseWriter, r *http.Request) {
	query := parseListQuery(r)
	userID, userErr := optionalUUID(query.UserID)
	keyID, keyErr := optionalUUID(query.GatewayKeyID)
	modelID, modelErr := optionalUUID(query.ModelID)
	poolID, poolErr := optionalUUID(query.ResourcePoolID)
	from, to, windowOK := a.requestLogWindow(query.From, query.To)
	if userErr != nil || keyErr != nil || modelErr != nil || poolErr != nil || !windowOK {
		a.writeError(w, r, quota.ErrInvalidInput)
		return
	}
	result, err := a.service.ListRequestLogs(r.Context(), principalFromContext(r.Context()), quota.RequestLogQuery{
		UserID: userID, GatewayKeyID: keyID, ModelID: modelID, ResourcePoolID: poolID, Search: query.Search,
		Status: quota.RequestStatus(query.Status), From: from, To: to, Page: quota.Page{Offset: query.offset(), Size: int32(query.PageSize)},
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	views, err := presentRequestLogs(principalFromContext(r.Context()), result.Items)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pageView[requestLogView]{Items: views, Page: query.Page, PageSize: query.PageSize, Total: result.Total})
}

func (a *QuotaAPI) getRequestLog(w http.ResponseWriter, r *http.Request) {
	requestID, err := uuid.Parse(chi.URLParam(r, "requestID"))
	if err != nil {
		a.writeError(w, r, quota.ErrInvalidInput)
		return
	}
	detail, err := a.service.GetRequestLog(r.Context(), principalFromContext(r.Context()), requestID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	view, err := presentRequestLogDetail(principalFromContext(r.Context()), detail)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, view)
}

func (a *QuotaAPI) requestLogWindow(fromValue, toValue string) (time.Time, time.Time, bool) {
	to := a.now().UTC()
	from := to.Add(-24 * time.Hour)
	var err error
	if strings.TrimSpace(toValue) != "" {
		to, err = time.Parse(time.RFC3339, toValue)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
	}
	if strings.TrimSpace(fromValue) != "" {
		from, err = time.Parse(time.RFC3339, fromValue)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
	} else if strings.TrimSpace(toValue) != "" {
		from = to.Add(-24 * time.Hour)
	}
	return from.UTC(), to.UTC(), to.After(from) && to.Sub(from) <= 31*24*time.Hour
}

func optionalUUID(value string) (*uuid.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (a *QuotaAPI) writeError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Usage operation failed.", Retryable: true, Stage: "quota"}
	switch {
	case errors.Is(err, quota.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_request", "Usage query is invalid.", false
	case errors.Is(err, quota.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot read these usage facts.", false
	case errors.Is(err, quota.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "Usage record was not found.", false
	default:
		if a.logger != nil {
			a.logger.Error("usage operation failed", "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
		}
	}
	writeProblem(w, r, value)
}

type ledgerEntryView struct {
	ID              string           `json:"id"`
	OccurredAt      time.Time        `json:"occurredAt"`
	OwnerName       string           `json:"ownerName"`
	SubscriptionID  string           `json:"subscriptionId"`
	ServicePlanName string           `json:"servicePlanName"`
	Kind            quota.LedgerKind `json:"kind"`
	TokenDelta      int64            `json:"tokenDelta"`
	Reason          string           `json:"reason"`
	RequestID       *string          `json:"requestId,omitempty"`
	ActorName       string           `json:"actorName"`
}

func presentLedgerEntries(principal identity.Principal, items []quota.LedgerEvent) ([]ledgerEntryView, error) {
	views := make([]ledgerEntryView, 0, len(items))
	for _, item := range items {
		if principal.Role == identity.RoleMember && item.UserID != principal.UserID {
			return nil, errors.New("member ledger scope invariant violated")
		}
		actorName := "系统"
		if item.CreatedBy != nil {
			actorName = "管理员"
			if principal.Role == identity.RoleAdministrator && item.ActorName != nil {
				actorName = *item.ActorName
			}
		}
		var requestID *string
		if item.RequestID != nil {
			value := item.RequestID.String()
			requestID = &value
		}
		views = append(views, ledgerEntryView{ID: item.ID.String(), OccurredAt: item.CreatedAt.UTC(), OwnerName: item.OwnerName,
			SubscriptionID: item.SubscriptionID.String(), ServicePlanName: item.ServicePlanName, Kind: item.Kind,
			TokenDelta: item.TokenDelta, Reason: ledgerReason(item), RequestID: requestID, ActorName: actorName})
	}
	return views, nil
}

func ledgerReason(item quota.LedgerEvent) string {
	if item.Note != nil && strings.TrimSpace(*item.Note) != "" {
		return strings.TrimSpace(*item.Note)
	}
	switch item.Kind {
	case quota.LedgerGrant:
		return "分配额度"
	case quota.LedgerReservation:
		return "请求预留"
	case quota.LedgerSettlement:
		return "用量结算"
	case quota.LedgerRelease:
		return "释放预留"
	case quota.LedgerCompensation:
		return "失败补偿"
	default:
		return "额度调整"
	}
}
