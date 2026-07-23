package controlapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	items, err := a.registry.ListProviders(r.Context(), principalFromContext(r.Context()))
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) getProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "providerID")
	if !ok {
		return
	}
	item, err := a.registry.GetProvider(r.Context(), principalFromContext(r.Context()), id)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) listModels(w http.ResponseWriter, r *http.Request) {
	items, err := a.registry.ListModels(r.Context(), principalFromContext(r.Context()))
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) listResourcePools(w http.ResponseWriter, r *http.Request) {
	includeRetired, _ := strconv.ParseBool(r.URL.Query().Get("includeRetired"))
	items, err := a.registry.ListResourcePools(r.Context(), principalFromContext(r.Context()), includeRetired)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) createResourcePool(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProviderID uuid.UUID   `json:"providerId"`
		Slug       string      `json:"slug"`
		Name       string      `json:"name"`
		ModelIDs   []uuid.UUID `json:"modelIds"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.registry.CreateResourcePool(r.Context(), principalFromContext(r.Context()), registry.NewResourcePool{ProviderID: input.ProviderID, Slug: input.Slug, Name: input.Name, ModelIDs: input.ModelIDs}, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, item)
}

func (a *API) updateResourcePool(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "resourcePoolID")
	if !ok {
		return
	}
	var input struct {
		Name              string    `json:"name"`
		ExpectedUpdatedAt time.Time `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.registry.UpdateResourcePool(r.Context(), principalFromContext(r.Context()), registry.ResourcePoolChange{ID: id, Name: input.Name, ExpectedUpdatedAt: input.ExpectedUpdatedAt}, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) setResourcePoolStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "resourcePoolID")
	if !ok {
		return
	}
	var input struct {
		Status            registry.ResourcePoolStatus `json:"status"`
		ExpectedUpdatedAt time.Time                   `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.registry.SetResourcePoolStatus(r.Context(), principalFromContext(r.Context()), id, input.Status, input.ExpectedUpdatedAt, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

type credentialInput struct {
	ResourcePoolID    uuid.UUID                         `json:"resourcePoolId"`
	Name              string                            `json:"name"`
	Secret            string                            `json:"secret"`
	RPMLimit          *int32                            `json:"rpmLimit"`
	TPMLimit          *int64                            `json:"tpmLimit"`
	ConcurrencyLimit  *int32                            `json:"concurrencyLimit"`
	ModelBindings     []registry.CredentialModelBinding `json:"modelBindings"`
	ExpectedUpdatedAt time.Time                         `json:"expectedUpdatedAt"`
}

func (a *API) listCredentials(w http.ResponseWriter, r *http.Request) {
	includeRetired, _ := strconv.ParseBool(r.URL.Query().Get("includeRetired"))
	items, err := a.registry.ListCredentials(r.Context(), principalFromContext(r.Context()), includeRetired)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) createCredential(w http.ResponseWriter, r *http.Request) {
	var input credentialInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		input.Secret = ""
		return
	}
	item, err := a.registry.CreateCredential(r.Context(), principalFromContext(r.Context()), registry.NewCredential{ResourcePoolID: input.ResourcePoolID, Name: input.Name, RPMLimit: input.RPMLimit, TPMLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit, ModelBindings: input.ModelBindings}, input.Secret, mutation)
	input.Secret = ""
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusCreated, item)
}

func (a *API) importCredentials(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ResourcePoolID uuid.UUID `json:"resourcePoolId"`
		Items          []struct {
			Name   string `json:"name"`
			Secret string `json:"secret"`
		} `json:"items"`
		ModelBindings    []registry.CredentialModelBinding `json:"modelBindings"`
		RPMLimit         *int32                            `json:"rpmLimit"`
		TPMLimit         *int64                            `json:"tpmLimit"`
		ConcurrencyLimit *int32                            `json:"concurrencyLimit"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		return
	}
	batch := make([]registry.CredentialBatchItem, 0, len(input.Items))
	for _, item := range input.Items {
		batch = append(batch, registry.CredentialBatchItem{Name: item.Name, Secret: item.Secret})
	}
	items, err := a.registry.ImportCredentials(r.Context(), principalFromContext(r.Context()), input.ResourcePoolID, batch, input.ModelBindings, input.RPMLimit, input.TPMLimit, input.ConcurrencyLimit, mutation)
	for index := range input.Items {
		input.Items[index].Secret = ""
	}
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (a *API) updateCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "credentialID")
	if !ok {
		return
	}
	var input credentialInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		input.Secret = ""
		return
	}
	item, err := a.registry.UpdateCredential(r.Context(), principalFromContext(r.Context()), registry.CredentialChange{ID: id, Name: input.Name, RPMLimit: input.RPMLimit, TPMLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit, ModelBindings: input.ModelBindings, ExpectedUpdatedAt: input.ExpectedUpdatedAt}, input.Secret, mutation)
	input.Secret = ""
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) setCredentialStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "credentialID")
	if !ok {
		return
	}
	var input struct {
		Status            registry.CredentialStatus `json:"status"`
		ExpectedUpdatedAt time.Time                 `json:"expectedUpdatedAt"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.registry.SetCredentialStatus(r.Context(), principalFromContext(r.Context()), id, input.Status, input.ExpectedUpdatedAt, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) retireCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "credentialID")
	if !ok {
		return
	}
	expectedUpdatedAt, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("expectedUpdatedAt"))
	if err != nil {
		writeDecodeError(w, r, err)
		return
	}
	mutation, ok := registryMutationRequest(w, r)
	if !ok {
		return
	}
	item, err := a.registry.RetireCredential(r.Context(), principalFromContext(r.Context()), id, expectedUpdatedAt, mutation)
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (a *API) probeCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "credentialID")
	if !ok {
		return
	}
	var input struct {
		ModelID uuid.UUID `json:"modelId"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	execution, credential, err := a.registry.ProbeCredential(r.Context(), principalFromContext(r.Context()), id, input.ModelID, httpserver.RequestIDFromContext(r.Context()))
	if err != nil {
		a.writeRegistryError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, struct {
		Execution  registry.CredentialProbeExecution `json:"execution"`
		Credential registry.Credential               `json:"credential"`
	}{execution, credential})
}

func registryMutationRequest(w http.ResponseWriter, r *http.Request) (registry.MutationRequest, bool) {
	idempotencyKey, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil || idempotencyKey == uuid.Nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID.", Stage: "registry"})
		return registry.MutationRequest{}, false
	}
	return registry.MutationRequest{IdempotencyKey: idempotencyKey, RequestID: httpserver.RequestIDFromContext(r.Context())}, true
}
