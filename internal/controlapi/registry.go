package controlapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

type providerKindView struct {
	Kind        providers.Kind           `json:"kind"`
	DisplayName string                   `json:"displayName"`
	Contract    providerKindContractView `json:"contract"`
}

type providerKindContractView struct {
	ReferenceURL      string                       `json:"referenceUrl"`
	ContractSnapshot  string                       `json:"contractSnapshot"`
	VerifiedAt        string                       `json:"verifiedAt"`
	ReferenceProvider string                       `json:"referenceProvider,omitempty"`
	VerifiedModels    []string                     `json:"verifiedModels"`
	LiveCapabilities  []string                     `json:"liveCapabilities"`
	Status            providers.VerificationStatus `json:"status"`
}

type providerPresetView struct {
	ID                   string                    `json:"id"`
	Name                 string                    `json:"name"`
	Kind                 providers.Kind            `json:"kind"`
	BaseURL              string                    `json:"baseUrl"`
	VerifiedAt           string                    `json:"verifiedAt"`
	Models               []providerPresetModelView `json:"models"`
	State                string                    `json:"state"`
	InstalledProviderID  *string                   `json:"installedProviderId,omitempty"`
	InstalledCredentials int                       `json:"installedCredentials"`
}

type providerPresetModelView struct {
	Alias           string   `json:"alias"`
	UpstreamModelID string   `json:"upstreamModelId"`
	Capabilities    []string `json:"capabilities"`
	ReasoningMode   string   `json:"reasoningMode,omitempty"`
	ContextTokens   int64    `json:"contextTokens"`
}

func (a *API) listProviderKinds(w http.ResponseWriter, r *http.Request) {
	kinds := providers.DefaultCatalog().Kinds()
	views := make([]providerKindView, 0, len(kinds))
	for _, kind := range kinds {
		views = append(views, providerKindView{Kind: kind.Kind, DisplayName: kind.DisplayName, Contract: providerKindContractView{
			ReferenceURL: kind.Contract.ReferenceURL, ContractSnapshot: kind.Contract.ContractSnapshot, VerifiedAt: kind.Contract.VerifiedAt,
			ReferenceProvider: kind.Contract.ReferenceProvider, VerifiedModels: kind.Contract.VerifiedModels,
			LiveCapabilities: kind.Contract.LiveCapabilities, Status: kind.Contract.Status,
		}})
	}
	writeData(w, http.StatusOK, views)
}

func (a *API) listProviderPresets(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	providersBySlug := make(map[string]registry.Provider, len(snapshot.providers))
	for _, provider := range snapshot.providers {
		providersBySlug[provider.Slug] = provider
	}
	presets := providers.DefaultCatalog().Presets()
	views := make([]providerPresetView, 0, len(presets))
	for _, preset := range presets {
		view := presentProviderPreset(preset)
		if installed, found := providersBySlug[preset.Slug]; found {
			providerID := installed.ID.String()
			view.InstalledProviderID = &providerID
			view.InstalledCredentials = snapshot.credentialCounts[installed.ID]
			view.State = "conflict"
			if installed.Kind == preset.Kind && installed.BaseURL == preset.BaseURL {
				view.State = "installed"
			}
		}
		views = append(views, view)
	}
	writeData(w, http.StatusOK, views)
}

func (a *API) installProviderPreset(w http.ResponseWriter, r *http.Request) {
	mutation, ok := providerMutationRequest(w, r)
	if !ok {
		return
	}
	installed, err := a.registry.InstallProviderPreset(r.Context(), principalFromContext(r.Context()), chi.URLParam(r, "presetID"), mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	models := make([]modelView, 0, len(installed.Models))
	snapshot := registrySnapshot{providerNames: map[uuid.UUID]string{installed.Provider.ID: installed.Provider.Name}}
	for _, model := range installed.Models {
		models = append(models, snapshot.presentModel(model))
	}
	writeData(w, http.StatusCreated, struct {
		PresetID string             `json:"presetId"`
		Provider providerRecordView `json:"provider"`
		Models   []modelView        `json:"models"`
	}{PresetID: installed.PresetID, Provider: presentProviderRecord(installed.Provider), Models: models})
}

func presentProviderPreset(preset providers.ProviderPreset) providerPresetView {
	models := make([]providerPresetModelView, 0, len(preset.Models))
	for _, model := range preset.Models {
		models = append(models, providerPresetModelView{
			Alias: model.PublicName, UpstreamModelID: model.UpstreamName,
			Capabilities: append([]string(nil), model.Capabilities...), ReasoningMode: model.ReasoningMode, ContextTokens: model.ContextTokens,
		})
	}
	return providerPresetView{
		ID: preset.ID, Name: preset.Name, Kind: preset.Kind, BaseURL: preset.BaseURL,
		VerifiedAt: preset.VerifiedAt, Models: models, State: "not_installed",
	}
}

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

func (a *API) getProvider(w http.ResponseWriter, r *http.Request) {
	providerID, err := uuid.Parse(chi.URLParam(r, "providerID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	provider, err := a.registry.GetProvider(r.Context(), principalFromContext(r.Context()), providerID)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentProviderRecord(provider))
}

func (a *API) createProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Slug    string         `json:"slug"`
		Name    string         `json:"name"`
		Kind    providers.Kind `json:"kind"`
		BaseURL string         `json:"baseUrl"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := providerMutationRequest(w, r)
	if !ok {
		return
	}
	created, err := a.registry.CreateProvider(r.Context(), principalFromContext(r.Context()), registry.Provider{
		Slug: input.Slug, Name: input.Name, Kind: input.Kind, BaseURL: input.BaseURL, Enabled: false,
	}, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, presentProviderRecord(created))
}

func (a *API) updateProvider(w http.ResponseWriter, r *http.Request) {
	providerID, err := uuid.Parse(chi.URLParam(r, "providerID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		Name              string         `json:"name"`
		Kind              providers.Kind `json:"kind"`
		BaseURL           string         `json:"baseUrl"`
		ExpectedUpdatedAt time.Time      `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := providerMutationRequest(w, r)
	if !ok {
		return
	}
	updated, err := a.registry.UpdateProvider(r.Context(), principalFromContext(r.Context()), registry.Provider{
		ID: providerID, Name: input.Name, Kind: input.Kind, BaseURL: input.BaseURL, UpdatedAt: input.ExpectedUpdatedAt,
	}, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentProviderRecord(updated))
}

func (a *API) setProviderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "providerID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		Enabled           *bool     `json:"enabled"`
		ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if input.Enabled == nil {
		a.writeRegistryError(w, r, registry.ErrInvalidInput)
		return
	}
	mutation, ok := providerMutationRequest(w, r)
	if !ok {
		return
	}
	updated, err := a.registry.SetProviderEnabled(r.Context(), principalFromContext(r.Context()), id, *input.Enabled, input.ExpectedUpdatedAt, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, presentProviderRecord(updated))
}

func providerMutationRequest(w http.ResponseWriter, r *http.Request) (registry.MutationRequest, bool) {
	idempotencyKey, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil || idempotencyKey == uuid.Nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID.", Stage: "registry"})
		return registry.MutationRequest{}, false
	}
	requestID := httpserver.RequestIDFromContext(r.Context())
	if requestID == "" {
		writeProblem(w, r, problem{Status: http.StatusInternalServerError, Code: "internal_invariant", Message: "Request identity is unavailable.", Stage: "registry", Retryable: true})
		return registry.MutationRequest{}, false
	}
	return registry.MutationRequest{IdempotencyKey: idempotencyKey, RequestID: requestID}, true
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
		ReasoningMode   registry.ReasoningMode  `json:"reasoningMode"`
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
	capabilities, err := modelCapabilities(input.Capabilities, input.ReasoningMode, *input.ContextTokens)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	provider, err := a.registry.GetProvider(r.Context(), principalFromContext(r.Context()), input.ProviderID)
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
	saved.ProviderName = provider.Name
	status := http.StatusOK
	if id == uuid.Nil {
		status = http.StatusCreated
	}
	writeData(w, status, (registrySnapshot{}).presentModel(saved))
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

type credentialModelBindingInput struct {
	ModelID  uuid.UUID `json:"modelId"`
	Priority int32     `json:"priority"`
	Weight   int32     `json:"weight"`
}

func registryCredentialBindings(inputs []credentialModelBindingInput) []registry.CredentialModelBinding {
	bindings := make([]registry.CredentialModelBinding, len(inputs))
	for index, input := range inputs {
		bindings[index] = registry.CredentialModelBinding{ModelID: input.ModelID, Priority: input.Priority, Weight: input.Weight}
	}
	return bindings
}

func (a *API) createCredential(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProviderID       uuid.UUID                     `json:"providerId"`
		Label            string                        `json:"label"`
		Secret           string                        `json:"secret"`
		ResourceDomain   registry.ResourceDomain       `json:"resourceDomain"`
		ModelBindings    []credentialModelBindingInput `json:"modelBindings"`
		RPMLimit         *int32                        `json:"rpmLimit"`
		TPMLimit         *int64                        `json:"tpmLimit"`
		ConcurrencyLimit *int32                        `json:"concurrencyLimit"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := providerMutationRequest(w, r)
	if !ok {
		return
	}
	provider, err := a.registry.GetProvider(r.Context(), principalFromContext(r.Context()), input.ProviderID)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	created, err := a.registry.CreateCredential(r.Context(), principalFromContext(r.Context()), registry.NewCredential{
		ProviderID:       input.ProviderID,
		Name:             input.Label,
		ResourceDomain:   input.ResourceDomain,
		RPMLimit:         input.RPMLimit,
		TPMLimit:         input.TPMLimit,
		ConcurrencyLimit: input.ConcurrencyLimit,
		ModelBindings:    registryCredentialBindings(input.ModelBindings),
	}, input.Secret, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	snapshot := registrySnapshot{providerNames: map[uuid.UUID]string{provider.ID: provider.Name}}
	writeData(w, http.StatusCreated, snapshot.presentCredential(created))
}

func (a *API) updateCredential(w http.ResponseWriter, r *http.Request) {
	credentialID, err := uuid.Parse(chi.URLParam(r, "credentialID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		Label             string                        `json:"label"`
		Secret            string                        `json:"secret"`
		ResourceDomain    registry.ResourceDomain       `json:"resourceDomain"`
		ModelBindings     []credentialModelBindingInput `json:"modelBindings"`
		RPMLimit          *int32                        `json:"rpmLimit"`
		TPMLimit          *int64                        `json:"tpmLimit"`
		ConcurrencyLimit  *int32                        `json:"concurrencyLimit"`
		ExpectedUpdatedAt time.Time                     `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := providerMutationRequest(w, r)
	if !ok {
		return
	}
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	updated, err := a.registry.UpdateCredential(r.Context(), principalFromContext(r.Context()), registry.CredentialChange{
		ID: credentialID, Name: input.Label, ResourceDomain: input.ResourceDomain,
		RPMLimit: input.RPMLimit, TPMLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit,
		ModelBindings: registryCredentialBindings(input.ModelBindings), ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	}, input.Secret, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, snapshot.presentCredential(updated))
}

func (a *API) setCredentialStatus(w http.ResponseWriter, r *http.Request) {
	credentialID, err := uuid.Parse(chi.URLParam(r, "credentialID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		Enabled           *bool     `json:"enabled"`
		ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if input.Enabled == nil {
		a.writeRegistryError(w, r, registry.ErrInvalidInput)
		return
	}
	mutation, ok := providerMutationRequest(w, r)
	if !ok {
		return
	}
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	updated, err := a.registry.SetCredentialEnabled(r.Context(), principalFromContext(r.Context()), credentialID, *input.Enabled, input.ExpectedUpdatedAt, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, snapshot.presentCredential(updated))
}

func (a *API) probeCredential(w http.ResponseWriter, r *http.Request) {
	credentialID, err := uuid.Parse(chi.URLParam(r, "credentialID"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	var input struct {
		ModelID string `json:"modelId"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	modelID, err := uuid.Parse(input.ModelID)
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	requestID := httpserver.RequestIDFromContext(r.Context())
	snapshot, err := a.loadRegistrySnapshot(r)
	if err != nil {
		a.writeRegistrySnapshotError(w, r, err)
		return
	}
	execution, credential, err := a.registry.ProbeCredential(r.Context(), principalFromContext(r.Context()), credentialID, modelID, requestID)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, credentialProbeView{
		Credential: snapshot.presentCredential(credential), Kind: execution.Kind, Status: execution.Status,
		ErrorKind: execution.ErrorKind, Retryable: execution.Retryable, MayUseTokens: execution.MayUseTokens,
		LatencyMillis: execution.LatencyMillis, ModelID: execution.ModelID.String(), ModelName: execution.ModelName,
		ResponseText: execution.ResponseText, InputTokens: execution.InputTokens, OutputTokens: execution.OutputTokens,
		RequestID: requestID,
	})
}
