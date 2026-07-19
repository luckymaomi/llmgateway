package controlapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	query := parseListQuery(r)
	views := make([]providerView, 0, len(snapshot.providers))
	for _, provider := range snapshot.providers {
		view := snapshot.presentProvider(provider)
		if query.Status != "" && view.Status != query.Status {
			continue
		}
		if !containsFold(view.Name+" "+string(view.Kind)+" "+view.BaseURL, query.Search) {
			continue
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) createProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name           string                  `json:"name"`
		Kind           providers.Kind          `json:"kind"`
		BaseURL        string                  `json:"baseUrl"`
		ResourceDomain registry.ResourceDomain `json:"resourceDomain"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	writeProblem(w, r, problem{
		Status:  http.StatusNotImplemented,
		Code:    "feature_not_implemented",
		Message: "Provider resource-domain persistence does not have a matching registry owner.",
		Stage:   "provider_resource_domain",
	})
}

func (a *API) updateProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name           string                  `json:"name"`
		Kind           providers.Kind          `json:"kind"`
		BaseURL        string                  `json:"baseUrl"`
		ResourceDomain registry.ResourceDomain `json:"resourceDomain"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	writeProblem(w, r, problem{
		Status:  http.StatusNotImplemented,
		Code:    "feature_not_implemented",
		Message: "Provider resource-domain persistence does not have a matching registry owner.",
		Stage:   "provider_resource_domain",
	})
}

func (a *API) setProviderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "providerID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	var selected *registry.Provider
	for index := range snapshot.providers {
		if snapshot.providers[index].ID == id {
			selected = &snapshot.providers[index]
			break
		}
	}
	if selected == nil {
		a.writeRegistryError(w, r, registry.ErrNotFound)
		return
	}
	selected.Enabled = input.Enabled
	updated, err := a.registry.UpdateProvider(r.Context(), principalFromContext(r.Context()), *selected)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, snapshot.presentProvider(updated))
}

func (a *API) listModels(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	query := parseListQuery(r)
	views := make([]modelView, 0, len(snapshot.models))
	for _, model := range snapshot.models {
		view := snapshot.presentModel(model)
		if query.Status != "" && view.Status != query.Status || query.ProviderID != "" && view.ProviderID != query.ProviderID || query.ResourceDomain != "" && string(view.ResourceDomain) != query.ResourceDomain {
			continue
		}
		if !containsFold(view.Alias+" "+view.UpstreamModelID+" "+view.ProviderName, query.Search) {
			continue
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) createModel(w http.ResponseWriter, r *http.Request) {
	a.writeModel(w, r, uuid.Nil)
}

func (a *API) updateModel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "modelID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	a.writeModel(w, r, id)
}

func (a *API) writeModel(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	var input struct {
		ProviderID      uuid.UUID               `json:"providerId"`
		Alias           string                  `json:"alias"`
		UpstreamModelID string                  `json:"upstreamModelId"`
		ResourceDomain  registry.ResourceDomain `json:"resourceDomain"`
		Capabilities    []string                `json:"capabilities"`
		ContextTokens   *int64                  `json:"contextTokens"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if input.ContextTokens == nil || *input.ContextTokens < 1 {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_request", Message: "contextTokens is required by the model registry.", Stage: "registry", FieldErrors: map[string]string{"contextTokens": "A positive context token limit is required."}})
		return
	}
	capabilities, err := modelCapabilities(input.Capabilities, *input.ContextTokens)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	model := registry.Model{
		ID:             id,
		ProviderID:     input.ProviderID,
		PublicName:     input.Alias,
		UpstreamName:   input.UpstreamModelID,
		DisplayName:    input.Alias,
		ResourceDomain: input.ResourceDomain,
		Capabilities:   capabilities,
		Enabled:        true,
	}
	var saved registry.Model
	err = nil
	if id == uuid.Nil {
		saved, err = a.registry.CreateModel(r.Context(), principalFromContext(r.Context()), model)
	} else {
		saved, err = a.registry.UpdateModel(r.Context(), principalFromContext(r.Context()), model)
	}
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	status := http.StatusOK
	if id == uuid.Nil {
		status = http.StatusCreated
	}
	writeData(w, status, snapshot.presentModel(saved))
}

func (a *API) listCredentials(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	query := parseListQuery(r)
	views := make([]credentialView, 0, len(snapshot.credentials))
	for _, credential := range snapshot.credentials {
		view := snapshot.presentCredential(credential)
		if query.Status != "" && string(view.Status) != query.Status || query.ProviderID != "" && view.ProviderID != query.ProviderID || query.ResourceDomain != "" && string(view.ResourceDomain) != query.ResourceDomain {
			continue
		}
		if !containsFold(view.Label+" "+view.ProviderName, query.Search) {
			continue
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, paginate(views, query))
}

func (a *API) createCredential(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProviderID       uuid.UUID               `json:"providerId"`
		Label            string                  `json:"label"`
		Secret           string                  `json:"secret"`
		ResourceDomain   registry.ResourceDomain `json:"resourceDomain"`
		AuthorizedModels []string                `json:"authorizedModels"`
		RPMLimit         *int32                  `json:"rpmLimit"`
		TPMLimit         *int64                  `json:"tpmLimit"`
		ConcurrencyLimit *int32                  `json:"concurrencyLimit"`
		FixedProxy       *string                 `json:"fixedProxy"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if len(input.AuthorizedModels) != 0 {
		writeProblem(w, r, problem{Status: http.StatusNotImplemented, Code: "feature_not_implemented", Message: "Atomic credential creation with model bindings does not have a registry owner yet.", Stage: "credential_model_authorization"})
		return
	}
	created, err := a.registry.CreateCredential(r.Context(), principalFromContext(r.Context()), registry.NewCredential{
		ProviderID:       input.ProviderID,
		Name:             input.Label,
		ResourceDomain:   input.ResourceDomain,
		RPMLimit:         input.RPMLimit,
		TPMLimit:         input.TPMLimit,
		ConcurrencyLimit: input.ConcurrencyLimit,
		FixedProxyURL:    input.FixedProxy,
	}, input.Secret)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, snapshot.presentCredential(created))
}
