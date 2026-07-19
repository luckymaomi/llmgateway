package controlapi

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

func TestRegistryContract(t *testing.T) {
	fixture := newControlFixture(t)
	providerID := uuid.New()
	fixture.registry.providers = []registry.Provider{{
		ID: providerID, Slug: "openai", Name: "OpenAI", Kind: providers.KindOpenAICompatible, BaseURL: "https://api.example.test/v1", Enabled: true, CreatedAt: fixture.now,
	}}
	fixture.registry.models = []registry.Model{{
		ID: uuid.New(), ProviderID: providerID, PublicName: "fast", UpstreamName: "fast-v1", DisplayName: "Fast", ResourceDomain: registry.ResourceProfessional,
		Capabilities: registry.ModelCapabilities{Chat: true, Streaming: true, ContextTokens: 32768, OutputTokens: 4096}, Enabled: true,
	}}
	fixture.registry.credentials = []registry.Credential{{
		ID: uuid.New(), ProviderID: providerID, Name: "Primary", ResourceDomain: registry.ResourceProfessional, Status: registry.CredentialActive,
	}}

	providersResponse := request(t, fixture.handler, http.MethodGet, "/api/control/providers?page=1&pageSize=20", nil, true, false)
	requireStatus(t, providersResponse, http.StatusOK)
	providersPage := decodeData[pageView[providerView]](t, providersResponse)
	if providersPage.Total != 1 || len(providersPage.Items) != 1 {
		t.Fatalf("unexpected providers page: %+v", providersPage)
	}
	provider := providersPage.Items[0]
	if provider.ModelCount != 1 || provider.CredentialCount != 1 || provider.ResourceDomain == nil || *provider.ResourceDomain != registry.ResourceProfessional || provider.RevisionID != fixture.activeID.String() {
		t.Fatalf("unexpected provider presentation: %+v", provider)
	}

	modelResponse := request(t, fixture.handler, http.MethodPost, "/api/control/models", map[string]any{
		"providerId":      providerID.String(),
		"alias":           "reasoning",
		"upstreamModelId": "reasoning-v2",
		"resourceDomain":  registry.ResourceProfessional,
		"capabilities":    []string{"streaming", "tools", "reasoning"},
		"contextTokens":   65536,
	}, true, true)
	requireStatus(t, modelResponse, http.StatusCreated)
	model := decodeData[modelView](t, modelResponse)
	if model.Alias != "reasoning" || model.ProviderName != "OpenAI" || model.ContextTokens == nil || *model.ContextTokens != 65536 {
		t.Fatalf("unexpected model: %+v", model)
	}
	if !fixture.registry.savedModel.Capabilities.Chat || !fixture.registry.savedModel.Capabilities.Tools || fixture.registry.savedModel.Capabilities.ContextTokens != 65536 {
		t.Fatalf("unexpected persisted capabilities: %+v", fixture.registry.savedModel.Capabilities)
	}

	statusResponse := request(t, fixture.handler, http.MethodPut, "/api/control/providers/"+providerID.String()+"/status", map[string]bool{"enabled": false}, true, true)
	requireStatus(t, statusResponse, http.StatusOK)
	updated := decodeData[providerView](t, statusResponse)
	if updated.Status != "disabled" || fixture.registry.updatedProvider.ID != providerID || fixture.registry.updatedProvider.Enabled {
		t.Fatalf("unexpected provider status: %+v", updated)
	}
}

func TestConfigurationContract(t *testing.T) {
	fixture := newControlFixture(t)

	revisionsResponse := request(t, fixture.handler, http.MethodGet, "/api/control/configuration/revisions?page=1&pageSize=20", nil, true, false)
	requireStatus(t, revisionsResponse, http.StatusOK)
	revisions := decodeData[pageView[configurationRevisionView]](t, revisionsResponse)
	if revisions.Total != 2 || revisions.Items[0].Status != "published" || revisions.Items[1].Status != "draft" {
		t.Fatalf("unexpected revisions: %+v", revisions)
	}

	validationResponse := request(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions/"+fixture.draftID.String()+"/validate", nil, true, true)
	requireStatus(t, validationResponse, http.StatusOK)
	validation := decodeData[operationView](t, validationResponse)
	if validation.Kind != "configuration.validate" || validation.Phase != "completed" || validation.Progress != 100 {
		t.Fatalf("unexpected validation operation: %+v", validation)
	}

	publishResponse := request(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions/"+fixture.draftID.String()+"/publish", map[string]string{
		"expectedActiveRevisionId": fixture.activeID.String(),
	}, true, true)
	requireStatus(t, publishResponse, http.StatusOK)
	publish := decodeData[operationView](t, publishResponse)
	if publish.Kind != "configuration.publish" || publish.Phase != "completed" || fixture.configuration.publishedID != fixture.draftID || fixture.configuration.expectedVersion != 7 {
		t.Fatalf("unexpected publish operation: %+v", publish)
	}
}
