package registry

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
)

func (s *Service) InstallProviderPreset(ctx context.Context, actor identity.Principal, presetID string, request MutationRequest) (ProviderPresetInstallation, error) {
	if !actor.CanOperateProviders() {
		return ProviderPresetInstallation{}, ErrForbidden
	}
	preset, found := providers.DefaultCatalog().Preset(presetID)
	if !found {
		return ProviderPresetInstallation{}, ErrNotFound
	}
	mutation, err := installProviderPresetMutation(request, preset)
	if err != nil {
		return ProviderPresetInstallation{}, err
	}
	if replayed, found, err := s.repository.ReplayProviderPresetInstallation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	installation, err := providerPresetInstallation(preset)
	if err != nil {
		return ProviderPresetInstallation{}, err
	}
	if err := s.validateProviderDetails(ctx, installation.Provider); err != nil {
		return ProviderPresetInstallation{}, err
	}
	if err := s.validateProviderSource(ctx, installation.Provider); err != nil {
		return ProviderPresetInstallation{}, err
	}
	for _, model := range installation.Models {
		if err := validateModel(model); err != nil {
			return ProviderPresetInstallation{}, err
		}
	}
	return s.repository.InstallProviderPreset(ctx, installation, actor.UserID, mutation)
}

func providerPresetInstallation(preset providers.ProviderPreset) (ProviderPresetInstallation, error) {
	verifiedAt, err := time.Parse(time.DateOnly, preset.VerifiedAt)
	if err != nil {
		return ProviderPresetInstallation{}, ErrInvalidInput
	}
	sourceURL := preset.SourceURL
	providerID := uuid.New()
	installation := ProviderPresetInstallation{
		PresetID: preset.ID,
		Provider: Provider{
			ID: providerID, Slug: preset.Slug, Name: preset.Name, Kind: preset.Kind, BaseURL: preset.BaseURL,
			Enabled: false, SourceURL: &sourceURL, VerifiedAt: &verifiedAt,
		},
		Models: make([]Model, 0, len(preset.Models)),
	}
	for _, source := range preset.Models {
		capabilities := ModelCapabilities{Chat: true, ContextTokens: source.ContextTokens, OutputTokens: source.ContextTokens}
		for _, capability := range source.Capabilities {
			switch capability {
			case "streaming":
				capabilities.Streaming = true
			case "tools":
				capabilities.Tools = true
			case "reasoning":
				capabilities.Reasoning = true
			case "structured_output":
				capabilities.StructuredOutput = true
			default:
				return ProviderPresetInstallation{}, ErrInvalidInput
			}
		}
		capabilities.ReasoningMode = ReasoningMode(source.ReasoningMode)
		installation.Models = append(installation.Models, Model{
			ID: uuid.New(), ProviderID: providerID, ProviderSlug: preset.Slug, ProviderName: preset.Name,
			PublicName: source.PublicName, UpstreamName: source.UpstreamName, DisplayName: source.DisplayName,
			ResourceDomain: ResourceDomain(source.ResourceDomain), Capabilities: capabilities, Enabled: true,
		})
	}
	return installation, nil
}
