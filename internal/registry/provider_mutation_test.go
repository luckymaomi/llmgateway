package registry

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/security"
)

func TestCommittedProviderMutationReplaysBeforeCurrentURLPolicy(t *testing.T) {
	replayed := Provider{ID: uuid.New(), Slug: "committed-provider", Name: "Committed Provider"}
	repository := &providerReplayRepository{replayed: replayed}
	envelope, err := security.NewEnvelopeCipher(1, map[uint32][]byte{1: bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	urls, err := security.NewURLValidator(security.SSRFPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, envelope, urls)
	if err != nil {
		t.Fatal(err)
	}
	actor := identity.Principal{UserID: uuid.New(), Role: identity.RoleAdministrator, Status: identity.StatusActive}
	request := MutationRequest{IdempotencyKey: uuid.New(), RequestID: "provider-replay-before-url-policy"}

	created, err := service.CreateProvider(context.Background(), actor, Provider{
		Slug: "committed-provider", Name: "Committed Provider", Kind: providers.KindOpenAICompatible, BaseURL: "https://127.0.0.1/v1",
	}, request)
	if err != nil || created.ID != replayed.ID {
		t.Fatalf("CreateProvider(replay) = %#v, %v", created, err)
	}
	updated, err := service.UpdateProvider(context.Background(), actor, Provider{
		ID: uuid.New(), Name: "Committed Provider", Kind: providers.KindOpenAICompatible, BaseURL: "https://127.0.0.1/v1", UpdatedAt: time.Now().UTC(),
	}, request)
	if err != nil || updated.ID != replayed.ID {
		t.Fatalf("UpdateProvider(replay) = %#v, %v", updated, err)
	}
	if repository.replayCalls != 2 {
		t.Fatalf("ReplayProviderMutation() calls = %d, want 2", repository.replayCalls)
	}
}

func TestCreateProviderFingerprintCoversPersistedSourceFacts(t *testing.T) {
	sourceURL := "https://source.example.test/provider"
	verifiedAt := time.Date(2026, time.July, 20, 8, 30, 0, 123, time.UTC)
	provider := Provider{
		Slug: "source-provider", Name: "Source Provider", Kind: providers.KindOpenAICompatible,
		BaseURL: "https://provider.example.test/v1", SourceURL: &sourceURL, VerifiedAt: &verifiedAt,
	}
	request := MutationRequest{IdempotencyKey: uuid.New(), RequestID: "provider-source-fingerprint"}
	baseline, err := createProviderMutation(request, provider)
	if err != nil {
		t.Fatal(err)
	}

	changedSource := provider
	otherSourceURL := "https://other.example.test/provider"
	changedSource.SourceURL = &otherSourceURL
	assertDifferentProviderFingerprint(t, baseline, request, changedSource)

	changedVerification := provider
	otherVerifiedAt := verifiedAt.Add(time.Nanosecond)
	changedVerification.VerifiedAt = &otherVerifiedAt
	assertDifferentProviderFingerprint(t, baseline, request, changedVerification)
}

func assertDifferentProviderFingerprint(t *testing.T, baseline ProviderMutation, request MutationRequest, provider Provider) {
	t.Helper()
	changed, err := createProviderMutation(request, provider)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(baseline.RequestFingerprint, changed.RequestFingerprint) {
		t.Fatal("Provider create fingerprint ignored a persisted source fact")
	}
}

type providerReplayRepository struct {
	Repository
	replayed    Provider
	replayCalls int
}

func (r *providerReplayRepository) ReplayProviderMutation(_ context.Context, _ uuid.UUID, _ ProviderMutation) (Provider, bool, error) {
	r.replayCalls++
	return r.replayed, true, nil
}
