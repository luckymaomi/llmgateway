package controlapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/quota"
)

type usageView struct {
	ID             string               `json:"id"`
	OccurredAt     time.Time            `json:"occurredAt"`
	UserName       string               `json:"userName"`
	KeyPrefix      string               `json:"keyPrefix"`
	ModelAlias     string               `json:"modelAlias"`
	ResourceDomain quota.ResourceDomain `json:"resourceDomain"`
	InputTokens    int64                `json:"inputTokens"`
	OutputTokens   int64                `json:"outputTokens"`
	UsageSource    quota.UsageSource    `json:"usageSource"`
	RequestID      string               `json:"requestId"`
}

func (a *QuotaAPI) presentUsage(ctx context.Context, principal identity.Principal, items []quota.UsageRecord) ([]usageView, error) {
	if len(items) == 0 {
		return []usageView{}, nil
	}
	userIDs := make([]uuid.UUID, 0, len(items))
	seen := make(map[uuid.UUID]struct{}, len(items))
	for _, item := range items {
		if _, exists := seen[item.UserID]; exists {
			continue
		}
		seen[item.UserID] = struct{}{}
		userIDs = append(userIDs, item.UserID)
	}
	names := map[uuid.UUID]string{}
	if principal.Role == identity.RoleMember {
		if len(userIDs) != 1 || userIDs[0] != principal.UserID {
			return nil, fmt.Errorf("quota presentation: member usage escaped the authenticated owner scope")
		}
		names[principal.UserID] = principal.DisplayName
	} else {
		var err error
		names, err = a.identity.UserDisplayNames(ctx, principal, userIDs)
		if err != nil {
			return nil, err
		}
	}
	views := make([]usageView, 0, len(items))
	for _, item := range items {
		userName := strings.TrimSpace(names[item.UserID])
		keyPrefix := strings.TrimSpace(item.KeyPrefix)
		modelAlias := strings.TrimSpace(item.ModelAlias)
		if userName == "" || keyPrefix == "" || modelAlias == "" {
			return nil, fmt.Errorf("quota presentation: usage %s has incomplete display facts", item.RequestID)
		}
		views = append(views, usageView{
			ID: item.RequestID.String(), OccurredAt: item.OccurredAt.UTC(), UserName: userName,
			KeyPrefix: keyPrefix, ModelAlias: modelAlias, ResourceDomain: item.ResourceDomain,
			InputTokens: item.InputTokens, OutputTokens: item.OutputTokens, UsageSource: item.UsageSource,
			RequestID: item.RequestID.String(),
		})
	}
	return views, nil
}
