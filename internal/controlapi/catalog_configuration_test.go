package controlapi

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

func TestRegistryContract(t *testing.T) {
	fixture := newControlFixture(t)
	kindsResponse := request(t, fixture.handler, http.MethodGet, "/api/control/provider-kinds", nil, true, false)
	requireStatus(t, kindsResponse, http.StatusOK)
	kinds := decodeData[[]providerKindView](t, kindsResponse)
	if len(kinds) != len(providers.DefaultCatalog().Kinds()) {
		t.Fatalf("unexpected Provider kind catalog: %+v", kinds)
	}
	for _, kind := range kinds {
		if kind.Contract.ReferenceURL == "" || kind.Contract.VerifiedAt == "" || len(kind.Contract.VerifiedModels) == 0 || len(kind.Contract.LiveCapabilities) == 0 {
			t.Fatalf("Provider kind contract is incomplete: %+v", kind)
		}
	}
	providerID := uuid.New()
	fixture.registry.providers = []registry.Provider{{
		ID: providerID, Slug: "openai", Name: "OpenAI", Kind: providers.KindOpenAICompatible, BaseURL: "https://api.example.test/v1", Enabled: false, CreatedAt: fixture.now, UpdatedAt: fixture.now,
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
	if provider.Slug != "openai" || provider.Status != "disabled" || provider.ModelCount != 1 || provider.CredentialCount != 1 || !provider.UpdatedAt.Equal(fixture.now) {
		t.Fatalf("unexpected provider presentation: %+v", provider)
	}
	providerResponse := request(t, fixture.handler, http.MethodGet, "/api/control/providers/"+providerID.String(), nil, true, false)
	requireStatus(t, providerResponse, http.StatusOK)
	providerRecord := decodeData[providerRecordView](t, providerResponse)
	if providerRecord.ID != providerID.String() || providerRecord.Slug != "openai" || providerRecord.Name != "OpenAI" || !providerRecord.UpdatedAt.Equal(fixture.now) {
		t.Fatalf("unexpected provider record: %+v", providerRecord)
	}

	createResponse := request(t, fixture.handler, http.MethodPost, "/api/control/providers", map[string]any{
		"slug": "agnes", "name": "Agnes", "kind": providers.KindAgnes, "baseUrl": "https://apihub.agnes-ai.com/v1",
	}, true, true)
	requireStatus(t, createResponse, http.StatusCreated)
	createdProvider := decodeData[providerView](t, createResponse)
	if createdProvider.Slug != "agnes" || createdProvider.Name != "Agnes" || createdProvider.Status != "disabled" || createdProvider.UpdatedAt.IsZero() {
		t.Fatalf("unexpected created provider: %+v", createdProvider)
	}

	updateResponse := request(t, fixture.handler, http.MethodPut, "/api/control/providers/"+providerID.String(), map[string]any{
		"name": "OpenAI Primary", "kind": providers.KindOpenAICompatible, "baseUrl": "https://api.example.test/v2", "expectedUpdatedAt": fixture.now,
	}, true, true)
	requireStatus(t, updateResponse, http.StatusOK)
	updatedProvider := decodeData[providerView](t, updateResponse)
	if updatedProvider.Slug != "openai" || updatedProvider.Name != "OpenAI Primary" || updatedProvider.Status != "disabled" || !updatedProvider.UpdatedAt.After(fixture.now) || fixture.registry.updatedProvider.BaseURL != "https://api.example.test/v2" {
		t.Fatalf("unexpected updated provider: %+v", updatedProvider)
	}

	modelInput := map[string]any{
		"providerId":      providerID.String(),
		"alias":           "reasoning",
		"upstreamModelId": "reasoning-v2",
		"resourceDomain":  registry.ResourceProfessional,
		"capabilities":    []string{"streaming", "tools", "reasoning"},
		"contextTokens":   65536,
	}
	invalidModelResponse := request(t, fixture.handler, http.MethodPost, "/api/control/models", modelInput, true, true)
	requireStatus(t, invalidModelResponse, http.StatusBadRequest)
	modelInput["reasoningMode"] = registry.ReasoningEffort
	modelResponse := request(t, fixture.handler, http.MethodPost, "/api/control/models", modelInput, true, true)
	requireStatus(t, modelResponse, http.StatusCreated)
	model := decodeData[modelView](t, modelResponse)
	if model.Alias != "reasoning" || model.ProviderName != "OpenAI Primary" || model.ContextTokens == nil || *model.ContextTokens != 65536 {
		t.Fatalf("unexpected model: %+v", model)
	}
	if !fixture.registry.savedModel.Capabilities.Chat || !fixture.registry.savedModel.Capabilities.Tools || fixture.registry.savedModel.Capabilities.ReasoningMode != registry.ReasoningEffort || fixture.registry.savedModel.Capabilities.ContextTokens != 65536 {
		t.Fatalf("unexpected persisted capabilities: %+v", fixture.registry.savedModel.Capabilities)
	}

	statusResponse := request(t, fixture.handler, http.MethodPut, "/api/control/providers/"+providerID.String()+"/status", map[string]any{"enabled": true, "expectedUpdatedAt": updatedProvider.UpdatedAt}, true, true)
	requireStatus(t, statusResponse, http.StatusOK)
	updated := decodeData[providerView](t, statusResponse)
	if updated.Status != "enabled" || !updated.UpdatedAt.After(updatedProvider.UpdatedAt) || fixture.registry.updatedProvider.ID != providerID || !fixture.registry.updatedProvider.Enabled {
		t.Fatalf("unexpected provider status: %+v", updated)
	}
}

func TestProviderPresetContractProjectsInstallationState(t *testing.T) {
	fixture := newControlFixture(t)
	listResponse := request(t, fixture.handler, http.MethodGet, "/api/control/provider-presets", nil, true, false)
	requireStatus(t, listResponse, http.StatusOK)
	presets := decodeData[[]providerPresetView](t, listResponse)
	if len(presets) != 4 {
		t.Fatalf("Provider preset count = %d, want 4", len(presets))
	}
	var agnes providerPresetView
	for _, preset := range presets {
		if preset.ID == "agnes" {
			agnes = preset
		}
	}
	if agnes.State != "not_installed" || agnes.InstalledProviderID != nil || len(agnes.Models) != 1 {
		t.Fatalf("initial Agnes preset = %#v", agnes)
	}

	installResponse := request(t, fixture.handler, http.MethodPost, "/api/control/provider-presets/agnes/install", nil, true, true)
	requireStatus(t, installResponse, http.StatusCreated)
	installation := decodeData[struct {
		PresetID string             `json:"presetId"`
		Provider providerRecordView `json:"provider"`
		Models   []modelView        `json:"models"`
	}](t, installResponse)
	if installation.PresetID != "agnes" || installation.Provider.Slug != "agnes" || installation.Provider.Status != "disabled" || len(installation.Models) != 1 {
		t.Fatalf("Provider preset installation = %#v", installation)
	}

	listResponse = request(t, fixture.handler, http.MethodGet, "/api/control/provider-presets", nil, true, false)
	requireStatus(t, listResponse, http.StatusOK)
	presets = decodeData[[]providerPresetView](t, listResponse)
	for _, preset := range presets {
		if preset.ID == "agnes" && (preset.State != "installed" || preset.InstalledProviderID == nil) {
			t.Fatalf("installed Agnes preset = %#v", preset)
		}
	}
}

func TestGetProviderAuthorizationAndMissingRecord(t *testing.T) {
	t.Run("member is forbidden", func(t *testing.T) {
		fixture := newControlFixture(t)
		fixture.identity.principal.Role = identity.RoleMember
		fixture.identity.principal.Status = identity.StatusActive

		response := request(t, fixture.handler, http.MethodGet, "/api/control/providers/"+uuid.NewString(), nil, true, false)
		requireStatus(t, response, http.StatusForbidden)
	})

	t.Run("missing provider is not found", func(t *testing.T) {
		fixture := newControlFixture(t)

		response := request(t, fixture.handler, http.MethodGet, "/api/control/providers/"+uuid.NewString(), nil, true, false)
		requireStatus(t, response, http.StatusNotFound)
	})
}

func TestConfigurationContract(t *testing.T) {
	fixture := newControlFixture(t)

	activeResponse := request(t, fixture.handler, http.MethodGet, "/api/control/configuration/active", nil, true, false)
	requireStatus(t, activeResponse, http.StatusOK)
	active := decodeData[activeConfigurationView](t, activeResponse)
	if active.RevisionID == nil || *active.RevisionID != fixture.activeID.String() || active.Version != 7 || active.UpdatedAt == nil || !active.UpdatedAt.Equal(fixture.now) || len(active.Models) != 1 ||
		active.Models[0].ID != fixture.activeModelID.String() || active.Models[0].Alias != "fast" || active.Models[0].ProviderName != "Published Provider" {
		t.Fatalf("unexpected active configuration: %+v", active)
	}

	revisionsResponse := request(t, fixture.handler, http.MethodGet, "/api/control/configuration/revisions?page=1&pageSize=20", nil, true, false)
	requireStatus(t, revisionsResponse, http.StatusOK)
	revisions := decodeData[pageView[configurationRevisionView]](t, revisionsResponse)
	if revisions.Total != 2 || revisions.Items[0].Status != "published" || revisions.Items[1].Status != "draft" || revisions.Items[0].CreatedBy != "Admin" || revisions.Items[1].CreatedBy != "Admin" {
		t.Fatalf("unexpected revisions: %+v", revisions)
	}

	validationResponse := request(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions/"+fixture.draftID.String()+"/validate", nil, true, true)
	requireStatus(t, validationResponse, http.StatusOK)
	validation := decodeData[operationView](t, validationResponse)
	if validation.Kind != "configuration.validate" || validation.Phase != "completed" || validation.Progress != 100 {
		t.Fatalf("unexpected validation operation: %+v", validation)
	}

	captureKey := uuid.New()
	captureResponse := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions", nil, true, true, captureKey.String())
	requireStatus(t, captureResponse, http.StatusCreated)
	captured := decodeData[configurationRevisionView](t, captureResponse)
	if captured.ID == "" || captured.Status != "draft" || captured.CreatedBy != "Admin" || captured.ProviderCount != 1 || captured.ModelCount != 1 || captured.CredentialCount != 1 || captured.RouteCount != 1 || captured.ValidationIssueCount != 0 {
		t.Fatalf("unexpected captured revision: %+v", captured)
	}
	if fixture.configuration.capturedMutation.IdempotencyKey != captureKey || fixture.configuration.capturedMutation.RequestID == "" {
		t.Fatalf("unexpected capture mutation: %+v", fixture.configuration.capturedMutation)
	}

	publishKey := uuid.New()
	publishResponse := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions/"+fixture.draftID.String()+"/publish", map[string]any{
		"expectedActiveVersion": int64(7),
	}, true, true, publishKey.String())
	requireStatus(t, publishResponse, http.StatusOK)
	publish := decodeData[operationView](t, publishResponse)
	if publish.Kind != "configuration.publish" || publish.Phase != "completed" || fixture.configuration.publishedID != fixture.draftID || fixture.configuration.expectedVersion != 7 ||
		fixture.configuration.publishedAction != configuration.MutationPublish || fixture.configuration.publishedMutation.IdempotencyKey != publishKey || fixture.configuration.publishedMutation.RequestID == "" {
		t.Fatalf("unexpected publish operation: %+v", publish)
	}
	publishedResult, ok := publish.Result.(map[string]any)
	if !ok || publishedResult["createdBy"] != "Admin" {
		t.Fatalf("unexpected publish result: %#v", publish.Result)
	}
}

func TestConfigurationRevisionListProjectsCreatorNamesAndSearch(t *testing.T) {
	fixture := newControlFixture(t)
	fixture.configuration.revisions[1].CreatedBy = fixture.memberID
	repeatedCreator := fixture.configuration.revisions[0]
	repeatedCreator.ID = uuid.New()
	repeatedCreator.Revision = 6
	repeatedCreator.CreatedAt = repeatedCreator.CreatedAt.Add(2 * time.Minute)
	fixture.configuration.revisions = append(fixture.configuration.revisions, repeatedCreator)
	response := request(t, fixture.handler, http.MethodGet, "/api/control/configuration/revisions?page=1&pageSize=20", nil, true, false)
	requireStatus(t, response, http.StatusOK)
	revisions := decodeData[pageView[configurationRevisionView]](t, response)
	if len(revisions.Items) != 3 || revisions.Items[0].CreatedBy != "Admin" || revisions.Items[1].CreatedBy != "Member" || revisions.Items[2].CreatedBy != "Admin" {
		t.Fatalf("configuration creator names = %+v", revisions.Items)
	}
	searchResponse := request(t, fixture.handler, http.MethodGet, "/api/control/configuration/revisions?search=Member&page=1&pageSize=20", nil, true, false)
	requireStatus(t, searchResponse, http.StatusOK)
	searchResult := decodeData[pageView[configurationRevisionView]](t, searchResponse)
	if searchResult.Total != 1 || len(searchResult.Items) != 1 || searchResult.Items[0].CreatedBy != "Member" {
		t.Fatalf("configuration creator search = %+v", searchResult)
	}
}

func TestConfigurationMutationResponsesDoNotReadIdentityAfterCommit(t *testing.T) {
	t.Run("capture uses authenticated identity", func(t *testing.T) {
		fixture := newControlFixture(t)
		fixture.identity.displayNameCalls = nil
		fixture.configuration.afterCapture = func() {
			fixture.identity.displayNameError = errors.New("identity unavailable after capture")
		}

		response := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions", nil, true, true, uuid.NewString())
		requireStatus(t, response, http.StatusCreated)
		captured := decodeData[configurationRevisionView](t, response)
		if captured.CreatedBy != "Admin" || len(fixture.identity.displayNameCalls) != 0 {
			t.Fatalf("capture creator/calls = %q/%v", captured.CreatedBy, fixture.identity.displayNameCalls)
		}
	})

	t.Run("publish resolves identity before mutation", func(t *testing.T) {
		fixture := newControlFixture(t)
		fixture.identity.displayNameCalls = nil
		fixture.configuration.afterPublish = func() {
			fixture.identity.displayNameError = errors.New("identity unavailable after publish")
		}

		response := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions/"+fixture.draftID.String()+"/publish", map[string]any{
			"expectedActiveVersion": int64(7),
		}, true, true, uuid.NewString())
		requireStatus(t, response, http.StatusOK)
		operation := decodeData[operationView](t, response)
		result, ok := operation.Result.(map[string]any)
		if !ok || result["createdBy"] != "Admin" || len(fixture.identity.displayNameCalls) != 1 {
			t.Fatalf("publish result/display-name calls = %#v/%v", operation.Result, fixture.identity.displayNameCalls)
		}
	})
}

func TestConfigurationRevisionListFailsWhenIdentityCannotResolveCreator(t *testing.T) {
	fixture := newControlFixture(t)
	fixture.configuration.revisions[0].CreatedBy = uuid.New()

	response := request(t, fixture.handler, http.MethodGet, "/api/control/configuration/revisions?page=1&pageSize=20", nil, true, false)
	requireStatus(t, response, http.StatusInternalServerError)
}

func TestConfigurationMutationRequiresIdempotencyKey(t *testing.T) {
	fixture := newControlFixture(t)

	captureResponse := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions", nil, true, true, "")
	requireStatus(t, captureResponse, http.StatusBadRequest)

	publishResponse := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions/"+fixture.draftID.String()+"/publish", map[string]any{
		"expectedActiveVersion": int64(7),
	}, true, true, "not-a-uuid")
	requireStatus(t, publishResponse, http.StatusBadRequest)
}

func TestConfigurationRollbackUsesCallerVersionAndMutation(t *testing.T) {
	fixture := newControlFixture(t)
	idempotencyKey := uuid.New()

	response := requestWithIdempotencyKey(t, fixture.handler, http.MethodPost, "/api/control/configuration/revisions/"+fixture.activeID.String()+"/rollback", map[string]any{
		"expectedActiveVersion": int64(7),
	}, true, true, idempotencyKey.String())
	requireStatus(t, response, http.StatusOK)
	operation := decodeData[operationView](t, response)
	if operation.Kind != "configuration.rollback" || operation.Phase != "completed" || fixture.configuration.publishedID != fixture.activeID || fixture.configuration.expectedVersion != 7 ||
		fixture.configuration.publishedAction != configuration.MutationRollback || fixture.configuration.publishedMutation.IdempotencyKey != idempotencyKey || fixture.configuration.publishedMutation.RequestID == "" {
		t.Fatalf("unexpected rollback operation: operation=%+v action=%q mutation=%+v", operation, fixture.configuration.publishedAction, fixture.configuration.publishedMutation)
	}
}
