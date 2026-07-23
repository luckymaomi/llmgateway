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

type entitlementView struct {
	ID               string               `json:"id"`
	OwnerID          string               `json:"ownerId"`
	OwnerName        string               `json:"ownerName"`
	PlanKind         quota.Plan           `json:"planKind"`
	ResourceDomain   quota.ResourceDomain `json:"resourceDomain"`
	ModelID          *string              `json:"modelId,omitempty"`
	ModelAlias       *string              `json:"modelAlias,omitempty"`
	GrantedTokens    int64                `json:"grantedTokens"`
	BalanceTokens    int64                `json:"balanceTokens"`
	RPMLimit         *int32               `json:"rpmLimit,omitempty"`
	TPMLimit         *int64               `json:"tpmLimit,omitempty"`
	ConcurrencyLimit int32                `json:"concurrencyLimit"`
	StartsAt         time.Time            `json:"startsAt"`
	ExpiresAt        time.Time            `json:"expiresAt"`
	Status           string               `json:"status"`
}

type entitlementInput struct {
	OwnerID          uuid.UUID            `json:"ownerId"`
	Plan             quota.Plan           `json:"planKind"`
	ResourceDomain   quota.ResourceDomain `json:"resourceDomain"`
	ModelID          *uuid.UUID           `json:"modelId"`
	GrantedTokens    int64                `json:"grantedTokens"`
	StartsAt         time.Time            `json:"startsAt"`
	ExpiresAt        time.Time            `json:"expiresAt"`
	ConcurrencyLimit int32                `json:"concurrencyLimit"`
	RPMLimit         *int32               `json:"rpmLimit"`
	TPMLimit         *int64               `json:"tpmLimit"`
	Reason           string               `json:"reason"`
}

func (a *QuotaAPI) presentEntitlements(ctx context.Context, principal identity.Principal, items []quota.Entitlement) ([]entitlementView, error) {
	if len(items) == 0 {
		return []entitlementView{}, nil
	}
	now := a.now().UTC()
	views := make([]entitlementView, 0, len(items))
	for _, item := range items {
		if principal.Role == identity.RoleMember && item.UserID != principal.UserID {
			return nil, fmt.Errorf("quota presentation: member entitlement escaped the authenticated owner scope")
		}
		ownerName := strings.TrimSpace(item.OwnerName)
		if ownerName == "" {
			return nil, fmt.Errorf("quota presentation: owner %s has no display name", item.UserID)
		}
		alias := item.ModelAlias
		if item.ModelID != nil {
			if alias == nil || strings.TrimSpace(*alias) == "" {
				return nil, fmt.Errorf("quota presentation: model %s has no public name", *item.ModelID)
			}
			value := strings.TrimSpace(*alias)
			alias = &value
		}
		views = append(views, presentEntitlement(item, ownerName, alias, now))
	}
	return views, nil
}

func (a *QuotaAPI) resolveOwnerName(ctx context.Context, principal identity.Principal, userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return "", quota.ErrInvalidInput
	}
	names, err := a.identity.UserDisplayNames(ctx, principal, []uuid.UUID{userID})
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(names[userID])
	if name == "" {
		return "", quota.ErrNotFound
	}
	return name, nil
}

func (a *QuotaAPI) resolveModelAlias(ctx context.Context, principal identity.Principal, modelID *uuid.UUID) (*string, error) {
	if modelID == nil {
		return nil, nil
	}
	if *modelID == uuid.Nil {
		return nil, quota.ErrInvalidInput
	}
	models, err := a.registry.ListModels(ctx, principal)
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		if model.ID == *modelID {
			alias := model.PublicName
			return &alias, nil
		}
	}
	return nil, quota.ErrNotFound
}

func presentEntitlement(value quota.Entitlement, ownerName string, modelAlias *string, now time.Time) entitlementView {
	status := "active"
	if now.Before(value.StartsAt) {
		status = "scheduled"
	} else if !now.Before(value.ExpiresAt) {
		status = "expired"
	}
	var modelID *string
	if value.ModelID != nil {
		encoded := value.ModelID.String()
		modelID = &encoded
	}
	return entitlementView{
		ID: value.ID.String(), OwnerID: value.UserID.String(), OwnerName: ownerName, PlanKind: value.Plan,
		ResourceDomain: value.ResourceDomain, ModelID: modelID, ModelAlias: modelAlias,
		GrantedTokens: value.GrantedTokens, BalanceTokens: value.BalanceTokens,
		RPMLimit: value.RPMLimit, TPMLimit: value.TPMLimit, ConcurrencyLimit: value.ConcurrencyLimit,
		StartsAt: value.StartsAt.UTC(), ExpiresAt: value.ExpiresAt.UTC(), Status: status,
	}
}
