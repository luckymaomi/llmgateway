package controlapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/subscription"
)

type planInput struct {
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Kind             subscription.PlanKind `json:"kind"`
	TokenQuota       int64                 `json:"tokenQuota"`
	ValidityDays     int32                 `json:"validityDays"`
	ConcurrencyLimit int32                 `json:"concurrencyLimit"`
	RPMLimit         *int32                `json:"rpmLimit"`
	TPMLimit         *int64                `json:"tpmLimit"`
	Routes           []struct {
		ModelID        uuid.UUID `json:"modelId"`
		ResourcePoolID uuid.UUID `json:"resourcePoolId"`
	} `json:"routes"`
}

func (a *API) listPlans(w http.ResponseWriter, r *http.Request) {
	includeArchived, _ := strconv.ParseBool(r.URL.Query().Get("includeArchived"))
	items, err := a.subscriptions.ListPlans(r.Context(), principalFromContext(r.Context()), includeArchived)
	if err != nil {
		a.writeSubscriptionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) publishPlan(w http.ResponseWriter, r *http.Request) {
	var input planInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	planID := uuid.Nil
	if value := chi.URLParam(r, "planID"); value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil {
			writeDecodeError(w, r, err)
			return
		}
		planID = parsed
	}
	routes := make([]subscription.PlanRoute, 0, len(input.Routes))
	for _, route := range input.Routes {
		routes = append(routes, subscription.PlanRoute{ModelID: route.ModelID, ResourcePoolID: route.ResourcePoolID})
	}
	mutation, ok := subscriptionMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.subscriptions.PublishPlan(r.Context(), principalFromContext(r.Context()), subscription.PlanDraft{
		ID: planID, Name: input.Name, Description: input.Description, Kind: input.Kind,
		TokenQuota: input.TokenQuota, ValidityDays: input.ValidityDays, ConcurrencyLimit: input.ConcurrencyLimit,
		RPMLimit: input.RPMLimit, TPMLimit: input.TPMLimit, Routes: routes,
	}, mutation)
	if err != nil {
		a.writeSubscriptionError(w, r, err)
		return
	}
	status := http.StatusCreated
	if planID != uuid.Nil {
		status = http.StatusOK
	}
	writeData(w, status, item)
}

func (a *API) setPlanStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "planID")
	if !ok {
		return
	}
	var input struct {
		Status subscription.PlanStatus `json:"status"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := subscriptionMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.subscriptions.SetPlanStatus(r.Context(), principalFromContext(r.Context()), id, input.Status, mutation)
	if err != nil {
		a.writeSubscriptionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	query := parseListQuery(r)
	var userID *uuid.UUID
	if query.UserID != "" {
		value, err := uuid.Parse(query.UserID)
		if err != nil {
			writeDecodeError(w, r, err)
			return
		}
		userID = &value
	}
	page, err := a.subscriptions.ListSubscriptions(r.Context(), principalFromContext(r.Context()), subscription.Query{UserID: userID, Search: query.Search, Status: query.Status, Offset: query.offset(), Size: int32(query.PageSize)})
	if err != nil {
		a.writeSubscriptionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, pageView[subscription.Subscription]{Items: page.Items, Total: page.Total, Page: query.Page, PageSize: query.PageSize})
}

func (a *API) createSubscription(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID        uuid.UUID `json:"userId"`
		ServicePlanID uuid.UUID `json:"servicePlanId"`
		GrantedTokens int64     `json:"grantedTokens"`
		StartsAt      time.Time `json:"startsAt"`
		ExpiresAt     time.Time `json:"expiresAt"`
		Notes         string    `json:"notes"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := subscriptionMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.subscriptions.CreateSubscription(r.Context(), principalFromContext(r.Context()), subscription.NewSubscription{UserID: input.UserID, ServicePlanID: input.ServicePlanID, GrantedTokens: input.GrantedTokens, StartsAt: input.StartsAt, ExpiresAt: input.ExpiresAt, Notes: input.Notes}, mutation)
	if err != nil {
		a.writeSubscriptionError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, item)
}

func (a *API) updateSubscription(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "subscriptionID")
	if !ok {
		return
	}
	var input struct {
		GrantedTokens     int64     `json:"grantedTokens"`
		StartsAt          time.Time `json:"startsAt"`
		ExpiresAt         time.Time `json:"expiresAt"`
		Notes             string    `json:"notes"`
		ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := subscriptionMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.subscriptions.UpdateSubscription(r.Context(), principalFromContext(r.Context()), subscription.SubscriptionChange{ID: id, GrantedTokens: input.GrantedTokens, StartsAt: input.StartsAt, ExpiresAt: input.ExpiresAt, Notes: input.Notes, ExpectedUpdatedAt: input.ExpectedUpdatedAt}, mutation)
	if err != nil {
		a.writeSubscriptionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) setSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "subscriptionID")
	if !ok {
		return
	}
	var input struct {
		Status            subscription.SubscriptionStatus `json:"status"`
		ExpectedUpdatedAt time.Time                       `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := subscriptionMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.subscriptions.SetSubscriptionStatus(r.Context(), principalFromContext(r.Context()), id, input.Status, input.ExpectedUpdatedAt, mutation)
	if err != nil {
		a.writeSubscriptionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func subscriptionMutationRequest(w http.ResponseWriter, r *http.Request) (subscription.MutationRequest, bool) {
	idempotencyKey, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil || idempotencyKey == uuid.Nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID.", Stage: "subscription"})
		return subscription.MutationRequest{}, false
	}
	return subscription.MutationRequest{IdempotencyKey: idempotencyKey, RequestID: httpserver.RequestIDFromContext(r.Context())}, true
}
