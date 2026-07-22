package controlapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

type identityStub struct {
	principal             identity.Principal
	credentials           identity.SessionCredentials
	registered            identity.User
	users                 []identity.User
	invitations           []identity.Invitation
	keys                  map[uuid.UUID][]identity.GatewayKey
	bootstrapped          bool
	bootstrapEmail        string
	changedPassword       bool
	registrationCode      string
	reviewedUserID        uuid.UUID
	reviewedStatus        identity.Status
	resetPasswordUserID   uuid.UUID
	resetPasswordMutation identity.MutationRequest
	revokedSessionsUserID uuid.UUID
	replacedKeyID         uuid.UUID
	createdInvitationAt   time.Time
	createdInvitationKey  identity.MutationRequest
	createdKeyOwnerID     uuid.UUID
	createdKeyName        string
	createdKeyModelIDs    []uuid.UUID
	createdKeyExpiresAt   *time.Time
	createdKeyMutation    identity.MutationRequest
	displayNameCalls      [][]uuid.UUID
	displayNameError      error
	loggedOut             bool
}

func (s *identityStub) IsBootstrapped(context.Context) (bool, error) {
	return s.bootstrapped, nil
}

func (s *identityStub) Bootstrap(_ context.Context, email string) (identity.BootstrapCredentials, error) {
	s.bootstrapEmail = email
	return identity.BootstrapCredentials{SessionCredentials: s.credentials, InitialPassword: "generated-initial-password"}, nil
}

func (s *identityStub) Register(_ context.Context, invitation, _, _, _ string) (identity.User, error) {
	s.registrationCode = invitation
	return s.registered, nil
}

func (s *identityStub) Login(context.Context, string, string) (identity.SessionCredentials, error) {
	return s.credentials, nil
}

func (s *identityStub) AuthenticateSession(context.Context, string) (identity.Principal, error) {
	return s.principal, nil
}

func (s *identityStub) UserDisplayNames(_ context.Context, _ identity.Principal, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	s.displayNameCalls = append(s.displayNameCalls, append([]uuid.UUID(nil), userIDs...))
	if s.displayNameError != nil {
		return nil, s.displayNameError
	}
	result := make(map[uuid.UUID]string, len(userIDs))
	for _, userID := range userIDs {
		for _, user := range s.users {
			if user.ID == userID {
				result[userID] = user.DisplayName
				break
			}
		}
	}
	return result, nil
}

func (s *identityStub) VerifyCSRF(_ identity.Principal, token string) bool {
	return token == "csrf-test-token"
}

func (s *identityStub) Logout(context.Context, identity.Principal) error {
	s.loggedOut = true
	return nil
}

func (s *identityStub) ChangePassword(_ context.Context, _ identity.Principal, currentPassword, replacementPassword, requestID string) (identity.SessionRevocation, error) {
	s.changedPassword = currentPassword == "current password" && replacementPassword == "replacement password" && requestID != ""
	return identity.SessionRevocation{RevokedSessions: 2}, nil
}

func (s *identityStub) ListUsers(_ context.Context, _ identity.Principal, status *identity.Status, page identity.Page) (identity.UserPage, error) {
	filtered := make([]identity.User, 0, len(s.users))
	for _, user := range s.users {
		if status == nil || user.Status == *status {
			filtered = append(filtered, user)
		}
	}
	start := int(page.Offset)
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + int(page.Size)
	if end > len(filtered) {
		end = len(filtered)
	}
	return identity.UserPage{Items: append([]identity.User(nil), filtered[start:end]...), Total: int64(len(filtered))}, nil
}

func (s *identityStub) SetUserStatus(_ context.Context, _ identity.Principal, userID uuid.UUID, status identity.Status) (identity.User, error) {
	s.reviewedUserID = userID
	s.reviewedStatus = status
	for index := range s.users {
		if s.users[index].ID == userID {
			s.users[index].Status = status
			return s.users[index], nil
		}
	}
	return identity.User{}, identity.ErrNotFound
}

func (s *identityStub) ResetMemberPassword(_ context.Context, _ identity.Principal, userID uuid.UUID, _ string, mutation identity.MutationRequest) (identity.SessionRevocation, error) {
	s.resetPasswordUserID = userID
	s.resetPasswordMutation = mutation
	return identity.SessionRevocation{RevokedSessions: 2}, nil
}

func (s *identityStub) RevokeUserSessions(_ context.Context, _ identity.Principal, userID uuid.UUID, _ string) (identity.SessionRevocation, error) {
	s.revokedSessionsUserID = userID
	return identity.SessionRevocation{RevokedSessions: 3}, nil
}

func (s *identityStub) CreateInvitation(_ context.Context, actor identity.Principal, expiresAt time.Time, mutation identity.MutationRequest) (identity.Invitation, error) {
	s.createdInvitationAt = expiresAt
	s.createdInvitationKey = mutation
	item := identity.Invitation{
		ID: uuid.New(), CreatedBy: actor.UserID, ExpiresAt: expiresAt.UTC(), CreatedAt: time.Now().UTC(),
		CodePrefix: "invite_once_s", Code: "invite_once_secret",
	}
	s.invitations = append(s.invitations, item)
	_ = actor
	return item, nil
}

func (s *identityStub) ListInvitations(_ context.Context, _ identity.Principal, page identity.Page) ([]identity.Invitation, error) {
	start := int(page.Offset)
	if start > len(s.invitations) {
		start = len(s.invitations)
	}
	end := start + int(page.Size)
	if end > len(s.invitations) {
		end = len(s.invitations)
	}
	return append([]identity.Invitation(nil), s.invitations[start:end]...), nil
}

func (s *identityStub) RevokeInvitation(_ context.Context, _ identity.Principal, invitationID uuid.UUID) error {
	for index := range s.invitations {
		if s.invitations[index].ID == invitationID {
			now := time.Now().UTC()
			s.invitations[index].RevokedAt = &now
			return nil
		}
	}
	return identity.ErrNotFound
}

func (s *identityStub) CreateGatewayKey(_ context.Context, _ identity.Principal, userID uuid.UUID, name string, authorizedModelIDs []uuid.UUID, expiresAt *time.Time, mutation identity.MutationRequest) (identity.GatewayKey, error) {
	s.createdKeyOwnerID = userID
	s.createdKeyName = name
	s.createdKeyModelIDs = append([]uuid.UUID(nil), authorizedModelIDs...)
	s.createdKeyExpiresAt = expiresAt
	s.createdKeyMutation = mutation
	item := identity.GatewayKey{
		ID:                 uuid.New(),
		UserID:             userID,
		Name:               name,
		Prefix:             "llmg_test_key",
		Secret:             "llmg_one_time_secret",
		AuthorizedModelIDs: append([]uuid.UUID(nil), authorizedModelIDs...),
		AuthorizedModels:   []string{"fast"},
		ExpiresAt:          expiresAt,
		CreatedAt:          time.Now().UTC(),
	}
	s.keys[userID] = append(s.keys[userID], item)
	return item, nil
}

func (s *identityStub) ReplaceGatewayKey(_ context.Context, _ identity.Principal, keyID uuid.UUID, _ identity.MutationRequest) (identity.GatewayKey, error) {
	s.replacedKeyID = keyID
	for ownerID, keys := range s.keys {
		for _, key := range keys {
			if key.ID != keyID {
				continue
			}
			return identity.GatewayKey{
				ID: uuid.New(), UserID: ownerID, Name: key.Name + " replacement", Prefix: "llmg_replace",
				Secret: "llmg_replacement_one_time_secret", AuthorizedModelIDs: append([]uuid.UUID(nil), key.AuthorizedModelIDs...),
				AuthorizedModels: append([]string(nil), key.AuthorizedModels...), ExpiresAt: key.ExpiresAt, CreatedAt: time.Now().UTC(),
			}, nil
		}
	}
	return identity.GatewayKey{}, identity.ErrNotFound
}

func (s *identityStub) ListGatewayKeys(_ context.Context, _ identity.Principal, userID uuid.UUID) ([]identity.GatewayKey, error) {
	return append([]identity.GatewayKey(nil), s.keys[userID]...), nil
}

func (s *identityStub) RevokeGatewayKey(_ context.Context, _ identity.Principal, keyID uuid.UUID) error {
	for ownerID := range s.keys {
		for index := range s.keys[ownerID] {
			if s.keys[ownerID][index].ID == keyID {
				now := time.Now().UTC()
				s.keys[ownerID][index].RevokedAt = &now
				return nil
			}
		}
	}
	return identity.ErrNotFound
}

type registryStub struct {
	providers       []registry.Provider
	models          []registry.Model
	credentials     []registry.Credential
	updatedProvider registry.Provider
	savedModel      registry.Model
	providerTime    time.Time
}

func (s *registryStub) InstallProviderPreset(_ context.Context, _ identity.Principal, presetID string, _ registry.MutationRequest) (registry.ProviderPresetInstallation, error) {
	preset, found := providers.DefaultCatalog().Preset(presetID)
	if !found {
		return registry.ProviderPresetInstallation{}, registry.ErrNotFound
	}
	provider := registry.Provider{
		ID: uuid.New(), Slug: preset.Slug, Name: preset.Name, Kind: preset.Kind, BaseURL: preset.BaseURL,
		UpdatedAt: s.nextProviderTime(time.Time{}),
	}
	provider.CreatedAt = provider.UpdatedAt
	models := make([]registry.Model, 0, len(preset.Models))
	for _, source := range preset.Models {
		model := registry.Model{
			ID: uuid.New(), ProviderID: provider.ID, ProviderName: provider.Name, PublicName: source.PublicName,
			UpstreamName: source.UpstreamName, DisplayName: source.DisplayName, ResourceDomain: registry.ResourceDomain(source.ResourceDomain),
			Capabilities: registry.ModelCapabilities{Chat: true, Streaming: true, Tools: true, Reasoning: source.ReasoningMode != "", ReasoningMode: registry.ReasoningMode(source.ReasoningMode), ContextTokens: source.ContextTokens, OutputTokens: source.ContextTokens}, Enabled: true,
		}
		models = append(models, model)
		s.models = append(s.models, model)
	}
	s.providers = append(s.providers, provider)
	return registry.ProviderPresetInstallation{PresetID: preset.ID, Provider: provider, Models: models}, nil
}

func (s *registryStub) CreateProvider(_ context.Context, _ identity.Principal, provider registry.Provider, _ registry.MutationRequest) (registry.Provider, error) {
	provider.ID = uuid.New()
	for _, current := range s.providers {
		if current.UpdatedAt.After(provider.UpdatedAt) {
			provider.UpdatedAt = current.UpdatedAt
		}
	}
	provider.UpdatedAt = s.nextProviderTime(provider.UpdatedAt)
	provider.CreatedAt = provider.UpdatedAt
	s.providers = append(s.providers, provider)
	return provider, nil
}

func (s *registryStub) UpdateProvider(_ context.Context, _ identity.Principal, provider registry.Provider, _ registry.MutationRequest) (registry.Provider, error) {
	for index := range s.providers {
		current := s.providers[index]
		if current.ID != provider.ID {
			continue
		}
		if !current.UpdatedAt.Equal(provider.UpdatedAt) {
			return registry.Provider{}, registry.ErrConflict
		}
		if current.Enabled && (current.Kind != provider.Kind || current.BaseURL != provider.BaseURL) {
			return registry.Provider{}, registry.ErrProviderEnabled
		}
		provider.Slug = current.Slug
		provider.Enabled = current.Enabled
		provider.SourceURL = current.SourceURL
		provider.VerifiedAt = current.VerifiedAt
		provider.CreatedAt = current.CreatedAt
		provider.UpdatedAt = s.nextProviderTime(current.UpdatedAt)
		s.providers[index] = provider
		s.updatedProvider = provider
		return provider, nil
	}
	return registry.Provider{}, registry.ErrNotFound
}

func (s *registryStub) SetProviderEnabled(_ context.Context, _ identity.Principal, providerID uuid.UUID, enabled bool, expectedUpdatedAt time.Time, _ registry.MutationRequest) (registry.Provider, error) {
	for index := range s.providers {
		current := s.providers[index]
		if current.ID != providerID {
			continue
		}
		if !current.UpdatedAt.Equal(expectedUpdatedAt) {
			return registry.Provider{}, registry.ErrConflict
		}
		current.Enabled = enabled
		current.UpdatedAt = s.nextProviderTime(current.UpdatedAt)
		s.providers[index] = current
		s.updatedProvider = current
		return current, nil
	}
	return registry.Provider{}, registry.ErrNotFound
}

func (s *registryStub) ListProviders(context.Context, identity.Principal) ([]registry.Provider, error) {
	return append([]registry.Provider(nil), s.providers...), nil
}

func (s *registryStub) GetProvider(_ context.Context, _ identity.Principal, providerID uuid.UUID) (registry.Provider, error) {
	for _, provider := range s.providers {
		if provider.ID == providerID {
			return provider, nil
		}
	}
	return registry.Provider{}, registry.ErrNotFound
}

func (s *registryStub) CreateModel(_ context.Context, _ identity.Principal, model registry.Model) (registry.Model, error) {
	model.ID = uuid.New()
	s.savedModel = model
	s.models = append(s.models, model)
	return model, nil
}

func (s *registryStub) UpdateModel(_ context.Context, _ identity.Principal, model registry.Model) (registry.Model, error) {
	s.savedModel = model
	return model, nil
}

func (s *registryStub) ListModels(context.Context, identity.Principal) ([]registry.Model, error) {
	return append([]registry.Model(nil), s.models...), nil
}

func (s *registryStub) CreateCredential(_ context.Context, _ identity.Principal, input registry.NewCredential, _ string, _ registry.MutationRequest) (registry.Credential, error) {
	item := registry.Credential{ID: uuid.New(), ProviderID: input.ProviderID, Name: input.Name, ResourceDomain: input.ResourceDomain, Status: registry.CredentialActive, ModelBindings: append([]registry.CredentialModelBinding(nil), input.ModelBindings...)}
	for bindingIndex, binding := range item.ModelBindings {
		for _, model := range s.models {
			if model.ID == binding.ModelID {
				item.ModelBindings[bindingIndex].ModelName = model.PublicName
			}
		}
	}
	s.credentials = append(s.credentials, item)
	return item, nil
}

func (s *registryStub) UpdateCredential(_ context.Context, _ identity.Principal, input registry.CredentialChange, _ string, _ registry.MutationRequest) (registry.Credential, error) {
	for index := range s.credentials {
		if s.credentials[index].ID != input.ID {
			continue
		}
		if !s.credentials[index].UpdatedAt.Equal(input.ExpectedUpdatedAt) {
			return registry.Credential{}, registry.ErrConflict
		}
		item := s.credentials[index]
		item.Name = input.Name
		item.ResourceDomain = input.ResourceDomain
		item.RPMLimit = input.RPMLimit
		item.TPMLimit = input.TPMLimit
		item.ConcurrencyLimit = input.ConcurrencyLimit
		item.ModelBindings = append([]registry.CredentialModelBinding(nil), input.ModelBindings...)
		item.UpdatedAt = s.nextProviderTime(item.UpdatedAt)
		s.credentials[index] = item
		return item, nil
	}
	return registry.Credential{}, registry.ErrNotFound
}

func (s *registryStub) SetCredentialEnabled(_ context.Context, _ identity.Principal, credentialID uuid.UUID, enabled bool, expectedUpdatedAt time.Time, _ registry.MutationRequest) (registry.Credential, error) {
	for index := range s.credentials {
		item := s.credentials[index]
		if item.ID != credentialID {
			continue
		}
		if !item.UpdatedAt.Equal(expectedUpdatedAt) {
			return registry.Credential{}, registry.ErrConflict
		}
		item.Status = registry.CredentialDisabled
		if enabled {
			item.Status = registry.CredentialActive
		}
		item.UpdatedAt = s.nextProviderTime(item.UpdatedAt)
		s.credentials[index] = item
		return item, nil
	}
	return registry.Credential{}, registry.ErrNotFound
}

func (s *registryStub) ProbeCredential(_ context.Context, _ identity.Principal, credentialID, modelID uuid.UUID, _ string) (registry.CredentialProbeExecution, registry.Credential, error) {
	for _, item := range s.credentials {
		if item.ID == credentialID {
			return registry.CredentialProbeExecution{Kind: "generation", Status: "succeeded", ModelID: modelID, MayUseTokens: true}, item, nil
		}
	}
	return registry.CredentialProbeExecution{}, registry.Credential{}, registry.ErrNotFound
}

func (s *registryStub) ListCredentials(context.Context, identity.Principal) ([]registry.Credential, error) {
	return append([]registry.Credential(nil), s.credentials...), nil
}

func (s *registryStub) nextProviderTime(current time.Time) time.Time {
	if s.providerTime.IsZero() {
		s.providerTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	if !current.IsZero() && !s.providerTime.After(current) {
		s.providerTime = current
	}
	s.providerTime = s.providerTime.Add(time.Millisecond)
	return s.providerTime
}

type configurationStub struct {
	active            configuration.Active
	activeErr         error
	catalog           configuration.Catalog
	revisions         []configuration.Revision
	capturedMutation  configuration.MutationRequest
	publishedID       uuid.UUID
	expectedVersion   int64
	publishedAction   configuration.MutationAction
	publishedMutation configuration.MutationRequest
	afterCapture      func()
	afterPublish      func()
}

func (s *configurationStub) Active(context.Context, identity.Principal) (configuration.Active, error) {
	return s.active, s.activeErr
}

func (s *configurationStub) ActiveCatalog(context.Context, identity.Principal) (configuration.Active, configuration.Catalog, error) {
	return s.active, s.catalog, s.activeErr
}

func (s *configurationStub) ListRevisions(_ context.Context, _ identity.Principal, offset, size int32) ([]configuration.Revision, error) {
	start := int(offset)
	if start > len(s.revisions) {
		start = len(s.revisions)
	}
	end := start + int(size)
	if end > len(s.revisions) {
		end = len(s.revisions)
	}
	return append([]configuration.Revision(nil), s.revisions[start:end]...), nil
}

func (s *configurationStub) CreateRevision(_ context.Context, actor identity.Principal, mutation configuration.MutationRequest) (configuration.Revision, error) {
	s.capturedMutation = mutation
	now := time.Now().UTC()
	revision := configuration.Revision{
		ID:        uuid.New(),
		Revision:  int64(len(s.revisions) + 1),
		Checksum:  "captured-checksum",
		Catalog:   configuration.CatalogSummary{ProviderCount: 1, ModelCount: 1, CredentialCount: 1, RouteCount: 1},
		CreatedBy: actor.UserID,
		CreatedAt: now,
	}
	s.revisions = append(s.revisions, revision)
	if s.afterCapture != nil {
		s.afterCapture()
	}
	return revision, nil
}

func (s *configurationStub) Publish(_ context.Context, _ identity.Principal, revisionID uuid.UUID, expectedVersion int64, action configuration.MutationAction, mutation configuration.MutationRequest) (configuration.Active, error) {
	s.publishedID = revisionID
	s.expectedVersion = expectedVersion
	s.publishedAction = action
	s.publishedMutation = mutation
	for _, revision := range s.revisions {
		if revision.ID == revisionID {
			now := time.Now().UTC()
			revision.PublishedAt = &now
			s.active = configuration.Active{Revision: revision, Version: expectedVersion + 1, UpdatedAt: now}
			s.activeErr = nil
			if s.afterPublish != nil {
				s.afterPublish()
			}
			return s.active, nil
		}
	}
	return configuration.Active{}, configuration.ErrNotFound
}
