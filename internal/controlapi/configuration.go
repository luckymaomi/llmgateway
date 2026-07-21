package controlapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type configurationRevisionView struct {
	ID                   string     `json:"id"`
	Sequence             int64      `json:"sequence"`
	Status               string     `json:"status"`
	CreatedBy            string     `json:"createdBy"`
	CreatedAt            time.Time  `json:"createdAt"`
	PublishedAt          *time.Time `json:"publishedAt,omitempty"`
	Summary              string     `json:"summary"`
	ValidationIssueCount int        `json:"validationIssueCount"`
	ProviderCount        int64      `json:"providerCount"`
	ModelCount           int64      `json:"modelCount"`
	CredentialCount      int64      `json:"credentialCount"`
	RouteCount           int64      `json:"routeCount"`
}

type activeConfigurationView struct {
	RevisionID *string                        `json:"revisionId"`
	Sequence   int64                          `json:"sequence"`
	Version    int64                          `json:"version"`
	UpdatedAt  *time.Time                     `json:"updatedAt"`
	Models     []activeConfigurationModelView `json:"models"`
}

type activeConfigurationModelView struct {
	ID             string `json:"id"`
	Alias          string `json:"alias"`
	DisplayName    string `json:"displayName"`
	ProviderID     string `json:"providerId"`
	ProviderName   string `json:"providerName"`
	ResourceDomain string `json:"resourceDomain"`
}

type operationView struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Phase     string    `json:"phase"`
	Step      string    `json:"step"`
	Progress  int       `json:"progress"`
	RequestID string    `json:"requestId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	CanCancel bool      `json:"canCancel"`
	Result    any       `json:"result,omitempty"`
}

func (a *API) listConfigurationRevisions(w http.ResponseWriter, r *http.Request) {
	revisions, err := a.collectConfigurationRevisions(r)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	active, activeErr := a.configuration.Active(r.Context(), principalFromContext(r.Context()))
	activeID := uuid.Nil
	if activeErr == nil {
		activeID = active.Revision.ID
	} else if !errors.Is(activeErr, configuration.ErrNotFound) {
		a.writeConfigurationError(w, r, activeErr)
		return
	}
	creatorNames, err := a.configurationCreatorNames(r.Context(), principalFromContext(r.Context()), revisions)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	query := parseListQuery(r)
	views := make([]configurationRevisionView, 0, len(revisions))
	for _, revision := range revisions {
		view := presentConfigurationRevision(revision, activeID, creatorNames[revision.CreatedBy])
		if query.Status != "" && view.Status != query.Status {
			continue
		}
		if !containsFold(view.Summary+" "+view.CreatedBy, query.Search) {
			continue
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) getActiveConfiguration(w http.ResponseWriter, r *http.Request) {
	active, catalog, err := a.configuration.ActiveCatalog(r.Context(), principalFromContext(r.Context()))
	if err != nil {
		if errors.Is(err, configuration.ErrNotFound) {
			writeData(w, http.StatusOK, activeConfigurationView{Version: 0, Models: []activeConfigurationModelView{}})
			return
		}
		a.writeConfigurationError(w, r, err)
		return
	}
	revisionID := active.Revision.ID.String()
	updatedAt := active.UpdatedAt.UTC()
	providerNames := make(map[uuid.UUID]string, len(catalog.Providers))
	for _, provider := range catalog.Providers {
		providerNames[provider.ID] = provider.Name
	}
	models := make([]activeConfigurationModelView, 0, len(catalog.Models))
	routedModels := make(map[uuid.UUID]struct{}, len(catalog.Routes))
	for _, route := range catalog.Routes {
		routedModels[route.ModelID] = struct{}{}
	}
	for _, model := range catalog.Models {
		if _, routed := routedModels[model.ID]; !routed {
			continue
		}
		models = append(models, activeConfigurationModelView{
			ID: model.ID.String(), Alias: model.PublicName, DisplayName: model.DisplayName,
			ProviderID: model.ProviderID.String(), ProviderName: providerNames[model.ProviderID], ResourceDomain: model.ResourceDomain,
		})
	}
	writeData(w, http.StatusOK, activeConfigurationView{RevisionID: &revisionID, Sequence: active.Revision.Revision, Version: active.Version, UpdatedAt: &updatedAt, Models: models})
}

func (a *API) captureConfigurationRevision(w http.ResponseWriter, r *http.Request) {
	principal := principalFromContext(r.Context())
	creatorName := strings.TrimSpace(principal.DisplayName)
	if creatorName == "" {
		a.writeIdentityError(w, r, fmt.Errorf("authenticated identity display name is unavailable"))
		return
	}
	mutation, ok := configurationMutationRequest(w, r)
	if !ok {
		return
	}
	revision, err := a.configuration.CreateRevision(r.Context(), principal, mutation)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, presentConfigurationRevision(revision, uuid.Nil, creatorName))
}

func (a *API) validateConfigurationRevision(w http.ResponseWriter, r *http.Request) {
	revision, err := a.findConfigurationRevision(r)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	if revision.Catalog.ModelCount == 0 || revision.Catalog.RouteCount == 0 {
		a.writeConfigurationError(w, r, configuration.ErrInvalidInput)
		return
	}
	creatorNames, err := a.configurationCreatorNames(r.Context(), principalFromContext(r.Context()), []configuration.Revision{revision})
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	now := time.Now().UTC()
	writeData(w, http.StatusOK, operationView{
		ID:        "configuration.validate." + revision.ID.String(),
		Kind:      "configuration.validate",
		Phase:     "completed",
		Step:      "Configuration validation completed.",
		Progress:  100,
		RequestID: httpserver.RequestIDFromContext(r.Context()),
		CreatedAt: now,
		UpdatedAt: now,
		CanCancel: false,
		Result:    presentConfigurationRevision(revision, uuid.Nil, creatorNames[revision.CreatedBy]),
	})
}

func (a *API) publishConfigurationRevision(w http.ResponseWriter, r *http.Request) {
	a.publishConfiguration(w, r, "publish")
}

func (a *API) rollbackConfigurationRevision(w http.ResponseWriter, r *http.Request) {
	a.publishConfiguration(w, r, "rollback")
}

func (a *API) publishConfiguration(w http.ResponseWriter, r *http.Request, action string) {
	revisionID, err := uuid.Parse(chi.URLParam(r, "revisionID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		ExpectedActiveVersion int64 `json:"expectedActiveVersion"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := configurationMutationRequest(w, r)
	if !ok {
		return
	}
	principal := principalFromContext(r.Context())
	targetRevision, err := a.findConfigurationRevision(r)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	creatorNames, err := a.configurationCreatorNames(r.Context(), principal, []configuration.Revision{targetRevision})
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	mutationAction := configuration.MutationPublish
	if action == "rollback" {
		mutationAction = configuration.MutationRollback
	}
	published, err := a.configuration.Publish(r.Context(), principal, revisionID, input.ExpectedActiveVersion, mutationAction, mutation)
	if err != nil {
		a.writeConfigurationError(w, r, err)
		return
	}
	step := "Configuration published."
	if action == "rollback" {
		step = "Configuration rollback completed."
	}
	writeData(w, http.StatusOK, operationView{
		ID:        "configuration." + action + "." + revisionID.String(),
		Kind:      "configuration." + action,
		Phase:     "completed",
		Step:      step,
		Progress:  100,
		RequestID: httpserver.RequestIDFromContext(r.Context()),
		CreatedAt: published.UpdatedAt.UTC(),
		UpdatedAt: published.UpdatedAt.UTC(),
		CanCancel: false,
		Result:    presentConfigurationRevision(published.Revision, published.Revision.ID, creatorNames[targetRevision.CreatedBy]),
	})
}

func configurationMutationRequest(w http.ResponseWriter, r *http.Request) (configuration.MutationRequest, bool) {
	idempotencyKey, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil || idempotencyKey == uuid.Nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID.", Stage: "configuration"})
		return configuration.MutationRequest{}, false
	}
	requestID := httpserver.RequestIDFromContext(r.Context())
	if requestID == "" {
		writeProblem(w, r, problem{Status: http.StatusInternalServerError, Code: "internal_invariant", Message: "Request identity is unavailable.", Stage: "configuration", Retryable: true})
		return configuration.MutationRequest{}, false
	}
	return configuration.MutationRequest{IdempotencyKey: idempotencyKey, RequestID: requestID}, true
}

func (a *API) findConfigurationRevision(r *http.Request) (configuration.Revision, error) {
	revisionID, err := uuid.Parse(chi.URLParam(r, "revisionID"))
	if err != nil {
		return configuration.Revision{}, configuration.ErrInvalidInput
	}
	revisions, err := a.collectConfigurationRevisions(r)
	if err != nil {
		return configuration.Revision{}, err
	}
	for _, revision := range revisions {
		if revision.ID == revisionID {
			return revision, nil
		}
	}
	return configuration.Revision{}, configuration.ErrNotFound
}

func (a *API) collectConfigurationRevisions(r *http.Request) ([]configuration.Revision, error) {
	principal := principalFromContext(r.Context())
	var all []configuration.Revision
	for offset := int32(0); ; offset += 200 {
		items, err := a.configuration.ListRevisions(r.Context(), principal, offset, 200)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < 200 {
			return all, nil
		}
	}
}

func (a *API) configurationCreatorNames(ctx context.Context, actor identity.Principal, revisions []configuration.Revision) (map[uuid.UUID]string, error) {
	creatorIDs := make([]uuid.UUID, 0, len(revisions))
	seen := make(map[uuid.UUID]struct{}, len(revisions))
	for _, revision := range revisions {
		if revision.CreatedBy == uuid.Nil {
			return nil, fmt.Errorf("configuration revision creator is unavailable")
		}
		if _, exists := seen[revision.CreatedBy]; exists {
			continue
		}
		seen[revision.CreatedBy] = struct{}{}
		creatorIDs = append(creatorIDs, revision.CreatedBy)
	}
	creatorNames, err := a.identity.UserDisplayNames(ctx, actor, creatorIDs)
	if err != nil {
		return nil, err
	}
	for _, creatorID := range creatorIDs {
		if strings.TrimSpace(creatorNames[creatorID]) == "" {
			return nil, fmt.Errorf("configuration revision creator display name is unavailable")
		}
	}
	return creatorNames, nil
}

func presentConfigurationRevision(revision configuration.Revision, activeID uuid.UUID, creatorName string) configurationRevisionView {
	status := "draft"
	if revision.ID == activeID {
		status = "published"
	} else if revision.PublishedAt != nil {
		status = "superseded"
	}
	issues := 0
	if revision.Catalog.ModelCount == 0 {
		issues++
	}
	if revision.Catalog.RouteCount == 0 {
		issues++
	}
	return configurationRevisionView{
		ID:                   revision.ID.String(),
		Sequence:             revision.Revision,
		Status:               status,
		CreatedBy:            creatorName,
		CreatedAt:            revision.CreatedAt.UTC(),
		PublishedAt:          utcTimePointer(revision.PublishedAt),
		Summary:              fmt.Sprintf("%d Provider / %d 模型 / %d 凭据", revision.Catalog.ProviderCount, revision.Catalog.ModelCount, revision.Catalog.CredentialCount),
		ValidationIssueCount: issues,
		ProviderCount:        revision.Catalog.ProviderCount,
		ModelCount:           revision.Catalog.ModelCount,
		CredentialCount:      revision.Catalog.CredentialCount,
		RouteCount:           revision.Catalog.RouteCount,
	}
}
