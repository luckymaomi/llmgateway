package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/quota"
)

type ledgerEntryView struct {
	ID             string               `json:"id"`
	OccurredAt     time.Time            `json:"occurredAt"`
	OwnerName      string               `json:"ownerName"`
	Kind           quota.LedgerKind     `json:"kind"`
	TokenDelta     int64                `json:"tokenDelta"`
	ResourceDomain quota.ResourceDomain `json:"resourceDomain"`
	Reason         string               `json:"reason"`
	RequestID      *string              `json:"requestId,omitempty"`
	ActorName      string               `json:"actorName"`
}

func (a *QuotaAPI) listLedgerEntries(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	query := parseListQuery(r)
	page, ok := quotaPage(query)
	if !ok {
		a.writeError(w, r, quota.ErrInvalidInput)
		return
	}
	result, err := a.service.ListLedger(r.Context(), principal, quota.LedgerFilter{
		Search: query.Search, ResourceDomain: quota.ResourceDomain(query.ResourceDomain), Page: page,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	views, err := a.presentLedgerEntries(r.Context(), principal, result.Items)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pageView[ledgerEntryView]{Items: views, Page: query.Page, PageSize: query.PageSize, Total: int(result.Total)})
}

func (a *QuotaAPI) presentLedgerEntries(ctx context.Context, principal identity.Principal, items []quota.LedgerEvent) ([]ledgerEntryView, error) {
	userIDs := make([]uuid.UUID, 0, len(items)*2)
	seen := make(map[uuid.UUID]struct{}, len(items)*2)
	for _, item := range items {
		for _, userID := range []*uuid.UUID{&item.UserID, item.CreatedBy} {
			if userID == nil {
				continue
			}
			if _, exists := seen[*userID]; !exists {
				seen[*userID] = struct{}{}
				userIDs = append(userIDs, *userID)
			}
		}
	}
	names, err := a.identity.UserDisplayNames(ctx, principal, userIDs)
	if err != nil {
		return nil, err
	}
	views := make([]ledgerEntryView, 0, len(items))
	for _, item := range items {
		ownerName := strings.TrimSpace(names[item.UserID])
		if ownerName == "" {
			return nil, fmt.Errorf("quota presentation: ledger owner %s has no display name", item.UserID)
		}
		actorName := "系统"
		if item.CreatedBy != nil {
			actorName = strings.TrimSpace(names[*item.CreatedBy])
			if actorName == "" {
				return nil, fmt.Errorf("quota presentation: ledger actor %s has no display name", *item.CreatedBy)
			}
		}
		reason := ledgerReason(item)
		var requestID *string
		if item.RequestID != nil {
			encoded := item.RequestID.String()
			requestID = &encoded
		}
		views = append(views, ledgerEntryView{
			ID: item.ID.String(), OccurredAt: item.CreatedAt.UTC(), OwnerName: ownerName, Kind: item.Kind,
			TokenDelta: item.TokenDelta, ResourceDomain: item.ResourceDomain, Reason: reason,
			RequestID: requestID, ActorName: actorName,
		})
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
		return "额度变更"
	}
}
