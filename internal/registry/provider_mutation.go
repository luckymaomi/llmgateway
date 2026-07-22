package registry

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/providers"
)

func createProviderMutation(request MutationRequest, provider Provider) (ProviderMutation, error) {
	return newProviderMutation(request, ProviderMutationCreate, struct {
		Slug       string  `json:"slug"`
		Name       string  `json:"name"`
		Kind       string  `json:"kind"`
		BaseURL    string  `json:"base_url"`
		SourceURL  *string `json:"source_url"`
		VerifiedAt *string `json:"verified_at"`
	}{
		Slug: provider.Slug, Name: provider.Name, Kind: string(provider.Kind), BaseURL: provider.BaseURL,
		SourceURL: provider.SourceURL, VerifiedAt: optionalMutationTime(provider.VerifiedAt),
	})
}

func updateProviderMutation(request MutationRequest, provider Provider) (ProviderMutation, error) {
	return newProviderMutation(request, ProviderMutationUpdate, struct {
		ProviderID       uuid.UUID `json:"provider_id"`
		Name             string    `json:"name"`
		Kind             string    `json:"kind"`
		BaseURL          string    `json:"base_url"`
		ExpectedRevision string    `json:"expected_revision"`
	}{
		ProviderID: provider.ID, Name: provider.Name, Kind: string(provider.Kind), BaseURL: provider.BaseURL,
		ExpectedRevision: provider.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

func statusProviderMutation(request MutationRequest, providerID uuid.UUID, enabled bool, expectedUpdatedAt time.Time) (ProviderMutation, error) {
	return newProviderMutation(request, ProviderMutationStatus, struct {
		ProviderID       uuid.UUID `json:"provider_id"`
		Enabled          bool      `json:"enabled"`
		ExpectedRevision string    `json:"expected_revision"`
	}{ProviderID: providerID, Enabled: enabled, ExpectedRevision: expectedUpdatedAt.UTC().Format(time.RFC3339Nano)})
}

func installProviderPresetMutation(request MutationRequest, preset providers.ProviderPreset) (ProviderMutation, error) {
	return newProviderMutation(request, ProviderMutationInstall, preset)
}

func optionalMutationTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func newProviderMutation(request MutationRequest, action ProviderMutationAction, input any) (ProviderMutation, error) {
	if request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return ProviderMutation{}, ErrInvalidInput
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return ProviderMutation{}, ErrInvalidInput
	}
	fingerprint := sha256.Sum256(encoded)
	return ProviderMutation{
		Action:             action,
		IdempotencyKey:     request.IdempotencyKey,
		RequestFingerprint: append([]byte(nil), fingerprint[:]...),
		RequestID:          request.RequestID,
	}, nil
}
