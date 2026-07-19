package controlapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

type providerView struct {
	ID              string                   `json:"id"`
	Name            string                   `json:"name"`
	Kind            providers.Kind           `json:"kind"`
	BaseURL         string                   `json:"baseUrl"`
	ResourceDomain  *registry.ResourceDomain `json:"resourceDomain,omitempty"`
	Status          string                   `json:"status"`
	ModelCount      int                      `json:"modelCount"`
	CredentialCount int                      `json:"credentialCount"`
	VerifiedAt      *time.Time               `json:"verifiedAt,omitempty"`
	RevisionID      string                   `json:"revisionId"`
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
	ID                string                    `json:"id"`
	ProviderID        string                    `json:"providerId"`
	ProviderName      string                    `json:"providerName"`
	Label             string                    `json:"label"`
	MaskedSecret      string                    `json:"maskedSecret"`
	ResourceDomain    registry.ResourceDomain   `json:"resourceDomain"`
	Status            registry.CredentialStatus `json:"status"`
	AuthorizedModels  []string                  `json:"authorizedModels"`
	RPMLimit          *int32                    `json:"rpmLimit,omitempty"`
	TPMLimit          *int64                    `json:"tpmLimit,omitempty"`
	ConcurrencyLimit  *int32                    `json:"concurrencyLimit,omitempty"`
	FixedProxy        *string                   `json:"fixedProxy,omitempty"`
	CooldownUntil     *time.Time                `json:"cooldownUntil,omitempty"`
	LastCheckedAt     *time.Time                `json:"lastCheckedAt,omitempty"`
	RecentSuccessRate *float64                  `json:"recentSuccessRate,omitempty"`
}

type registrySnapshot struct {
	providers        []registry.Provider
	models           []registry.Model
	credentials      []registry.Credential
	providerNames    map[uuid.UUID]string
	modelCounts      map[uuid.UUID]int
	credentialCounts map[uuid.UUID]int
	providerDomains  map[uuid.UUID]*registry.ResourceDomain
	revisionID       string
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
		providerDomains:  make(map[uuid.UUID]*registry.ResourceDomain),
	}
	domains := make(map[uuid.UUID]map[registry.ResourceDomain]struct{})
	for _, provider := range providersList {
		snapshot.providerNames[provider.ID] = provider.Name
	}
	for _, model := range models {
		snapshot.modelCounts[model.ProviderID]++
		addDomain(domains, model.ProviderID, model.ResourceDomain)
	}
	for _, credential := range credentials {
		snapshot.credentialCounts[credential.ProviderID]++
		addDomain(domains, credential.ProviderID, credential.ResourceDomain)
	}
	for providerID, values := range domains {
		if len(values) == 1 {
			for domain := range values {
				value := domain
				snapshot.providerDomains[providerID] = &value
			}
		}
	}
	active, activeErr := a.configuration.Active(r.Context(), principal)
	if activeErr == nil {
		snapshot.revisionID = active.Revision.ID.String()
	} else if !errors.Is(activeErr, configuration.ErrNotFound) {
		return registrySnapshot{}, activeErr
	}
	return snapshot, nil
}

func (s registrySnapshot) presentProvider(provider registry.Provider) providerView {
	status := "disabled"
	if provider.Enabled {
		status = "active"
	}
	return providerView{
		ID:              provider.ID.String(),
		Name:            provider.Name,
		Kind:            provider.Kind,
		BaseURL:         provider.BaseURL,
		ResourceDomain:  s.providerDomains[provider.ID],
		Status:          status,
		ModelCount:      s.modelCounts[provider.ID],
		CredentialCount: s.credentialCounts[provider.ID],
		VerifiedAt:      utcTimePointer(provider.VerifiedAt),
		RevisionID:      s.revisionID,
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
	return credentialView{
		ID:               credential.ID.String(),
		ProviderID:       credential.ProviderID.String(),
		ProviderName:     s.providerNames[credential.ProviderID],
		Label:            credential.Name,
		MaskedSecret:     "********",
		ResourceDomain:   credential.ResourceDomain,
		Status:           credential.Status,
		AuthorizedModels: []string{},
		RPMLimit:         credential.RPMLimit,
		TPMLimit:         credential.TPMLimit,
		ConcurrencyLimit: credential.ConcurrencyLimit,
		FixedProxy:       credential.FixedProxyURL,
		CooldownUntil:    utcTimePointer(credential.CooldownUntil),
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

func addDomain(domains map[uuid.UUID]map[registry.ResourceDomain]struct{}, providerID uuid.UUID, domain registry.ResourceDomain) {
	if domains[providerID] == nil {
		domains[providerID] = make(map[registry.ResourceDomain]struct{})
	}
	domains[providerID][domain] = struct{}{}
}
