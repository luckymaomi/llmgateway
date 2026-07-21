package controlapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

type providerRecordView struct {
	ID         string         `json:"id"`
	Slug       string         `json:"slug"`
	Name       string         `json:"name"`
	Kind       providers.Kind `json:"kind"`
	BaseURL    string         `json:"baseUrl"`
	Status     string         `json:"status"`
	VerifiedAt *time.Time     `json:"verifiedAt,omitempty"`
	UpdatedAt  time.Time      `json:"updatedAt"`
}

type providerView struct {
	providerRecordView
	ModelCount      int `json:"modelCount"`
	CredentialCount int `json:"credentialCount"`
}

type modelView struct {
	ID              string                  `json:"id"`
	ProviderID      string                  `json:"providerId"`
	ProviderName    string                  `json:"providerName"`
	Alias           string                  `json:"alias"`
	UpstreamModelID string                  `json:"upstreamModelId"`
	ResourceDomain  registry.ResourceDomain `json:"resourceDomain"`
	Capabilities    []string                `json:"capabilities"`
	ContextTokens   *int64                  `json:"contextTokens,omitempty"`
	Status          string                  `json:"status"`
}

type credentialView struct {
	ID                 string                    `json:"id"`
	ProviderID         string                    `json:"providerId"`
	ProviderName       string                    `json:"providerName"`
	Label              string                    `json:"label"`
	MaskedSecret       string                    `json:"maskedSecret"`
	ResourceDomain     registry.ResourceDomain   `json:"resourceDomain"`
	Status             registry.CredentialStatus `json:"status"`
	AuthorizedModelIDs []string                  `json:"authorizedModelIds"`
	AuthorizedModels   []string                  `json:"authorizedModels"`
	RPMLimit           *int32                    `json:"rpmLimit,omitempty"`
	TPMLimit           *int64                    `json:"tpmLimit,omitempty"`
	ConcurrencyLimit   *int32                    `json:"concurrencyLimit,omitempty"`
	CooldownUntil      *time.Time                `json:"cooldownUntil,omitempty"`
	LastCheckedAt      *time.Time                `json:"lastCheckedAt,omitempty"`
	RecentSuccessRate  *float64                  `json:"recentSuccessRate,omitempty"`
	LastProbeAt        *time.Time                `json:"lastProbeAt,omitempty"`
	LastProbeLatencyMs *int64                    `json:"lastProbeLatencyMs,omitempty"`
	LastProbeKind      *string                   `json:"lastProbeKind,omitempty"`
	LastProbeStatus    *string                   `json:"lastProbeStatus,omitempty"`
	LastProbeErrorKind *string                   `json:"lastProbeErrorKind,omitempty"`
	UpdatedAt          time.Time                 `json:"updatedAt"`
}

type credentialProbeView struct {
	Credential    credentialView `json:"credential"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	ErrorKind     *string        `json:"errorKind,omitempty"`
	Retryable     bool           `json:"retryable"`
	MayUseTokens  bool           `json:"mayUseTokens"`
	LatencyMillis int64          `json:"latencyMillis"`
	RequestID     string         `json:"requestId"`
}

type registrySnapshot struct {
	providers        []registry.Provider
	models           []registry.Model
	credentials      []registry.Credential
	providerNames    map[uuid.UUID]string
	modelCounts      map[uuid.UUID]int
	credentialCounts map[uuid.UUID]int
}

func (a *API) loadRegistrySnapshot(r *http.Request) (registrySnapshot, error) {
	principal := principalFromContext(r.Context())
	providersList, err := a.registry.ListProviders(r.Context(), principal)
	if err != nil {
		return registrySnapshot{}, err
	}
	models, err := a.registry.ListModels(r.Context(), principal)
	if err != nil {
		return registrySnapshot{}, err
	}
	credentials, err := a.registry.ListCredentials(r.Context(), principal)
	if err != nil {
		return registrySnapshot{}, err
	}
	snapshot := registrySnapshot{
		providers:        providersList,
		models:           models,
		credentials:      credentials,
		providerNames:    make(map[uuid.UUID]string, len(providersList)),
		modelCounts:      make(map[uuid.UUID]int),
		credentialCounts: make(map[uuid.UUID]int),
	}
	for _, provider := range providersList {
		snapshot.providerNames[provider.ID] = provider.Name
	}
	for _, model := range models {
		snapshot.modelCounts[model.ProviderID]++
	}
	for _, credential := range credentials {
		snapshot.credentialCounts[credential.ProviderID]++
	}
	return snapshot, nil
}

func (s registrySnapshot) presentProvider(provider registry.Provider) providerView {
	return providerView{
		providerRecordView: presentProviderRecord(provider),
		ModelCount:         s.modelCounts[provider.ID],
		CredentialCount:    s.credentialCounts[provider.ID],
	}
}

func presentProviderRecord(provider registry.Provider) providerRecordView {
	status := "disabled"
	if provider.Enabled {
		status = "enabled"
	}
	return providerRecordView{
		ID:         provider.ID.String(),
		Slug:       provider.Slug,
		Name:       provider.Name,
		Kind:       provider.Kind,
		BaseURL:    provider.BaseURL,
		Status:     status,
		VerifiedAt: utcTimePointer(provider.VerifiedAt),
		UpdatedAt:  provider.UpdatedAt.UTC(),
	}
}

func (s registrySnapshot) presentModel(model registry.Model) modelView {
	status := "disabled"
	if model.Enabled {
		status = "active"
	}
	capabilities := make([]string, 0, 4)
	if model.Capabilities.Streaming {
		capabilities = append(capabilities, "streaming")
	}
	if model.Capabilities.Tools {
		capabilities = append(capabilities, "tools")
	}
	if model.Capabilities.Reasoning {
		capabilities = append(capabilities, "reasoning")
	}
	if model.Capabilities.StructuredOutput {
		capabilities = append(capabilities, "structured_output")
	}
	providerName := model.ProviderName
	if providerName == "" {
		providerName = s.providerNames[model.ProviderID]
	}
	contextTokens := model.Capabilities.ContextTokens
	return modelView{
		ID:              model.ID.String(),
		ProviderID:      model.ProviderID.String(),
		ProviderName:    providerName,
		Alias:           model.PublicName,
		UpstreamModelID: model.UpstreamName,
		ResourceDomain:  model.ResourceDomain,
		Capabilities:    capabilities,
		ContextTokens:   &contextTokens,
		Status:          status,
	}
}

func (s registrySnapshot) presentCredential(credential registry.Credential) credentialView {
	modelIDs := make([]string, 0, len(credential.AuthorizedModelIDs))
	for _, modelID := range credential.AuthorizedModelIDs {
		modelIDs = append(modelIDs, modelID.String())
	}
	return credentialView{
		ID:                 credential.ID.String(),
		ProviderID:         credential.ProviderID.String(),
		ProviderName:       s.providerNames[credential.ProviderID],
		Label:              credential.Name,
		MaskedSecret:       "********",
		ResourceDomain:     credential.ResourceDomain,
		Status:             credential.Status,
		AuthorizedModelIDs: modelIDs,
		AuthorizedModels:   append([]string(nil), credential.AuthorizedModels...),
		RPMLimit:           credential.RPMLimit,
		TPMLimit:           credential.TPMLimit,
		ConcurrencyLimit:   credential.ConcurrencyLimit,
		CooldownUntil:      utcTimePointer(credential.CooldownUntil),
		LastProbeAt:        utcTimePointer(credential.LastProbeAt),
		LastProbeLatencyMs: credential.LastProbeLatencyMs,
		LastProbeKind:      credential.LastProbeKind,
		LastProbeStatus:    credential.LastProbeStatus,
		LastProbeErrorKind: credential.LastProbeErrorKind,
		UpdatedAt:          credential.UpdatedAt.UTC(),
	}
}

func modelCapabilities(values []string, contextTokens int64) (registry.ModelCapabilities, error) {
	capabilities := registry.ModelCapabilities{Chat: true, ContextTokens: contextTokens, OutputTokens: contextTokens}
	for _, value := range values {
		switch value {
		case "streaming":
			capabilities.Streaming = true
		case "tools":
			capabilities.Tools = true
		case "reasoning":
			capabilities.Reasoning = true
		case "structured_output":
			capabilities.StructuredOutput = true
		default:
			return registry.ModelCapabilities{}, registry.ErrInvalidInput
		}
	}
	return capabilities, nil
}
