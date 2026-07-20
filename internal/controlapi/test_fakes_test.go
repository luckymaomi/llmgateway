package controlapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

type identityStub struct {
	principal            identity.Principal
	credentials          identity.SessionCredentials
	registered           identity.User
	users                []identity.User
	invitations          []identity.Invitation
	keys                 map[uuid.UUID][]identity.GatewayKey
	bootstrapped         bool
	bootstrapEmail       string
	bootstrapName        string
	registrationCode     string
	reviewedUserID       uuid.UUID
	reviewedStatus       identity.Status
	createdInvitationFor time.Duration
	loggedOut            bool
}

func (s *identityStub) IsBootstrapped(context.Context) (bool, error) {
	return s.bootstrapped, nil
}

func (s *identityStub) Bootstrap(_ context.Context, email, displayName, _ string) (identity.SessionCredentials, error) {
	s.bootstrapEmail = email
	s.bootstrapName = displayName
	return s.credentials, nil
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

func (s *identityStub) VerifyCSRF(_ identity.Principal, token string) bool {
	return token == "csrf-test-token"
}

func (s *identityStub) Logout(context.Context, identity.Principal) error {
	s.loggedOut = true
	return nil
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

func (s *identityStub) CreateInvitation(_ context.Context, actor identity.Principal, role identity.Role, validFor time.Duration) (identity.Invitation, error) {
	s.createdInvitationFor = validFor
	item := identity.Invitation{ID: uuid.New(), Role: role, ExpiresAt: time.Now().UTC().Add(validFor), CreatedAt: time.Now().UTC(), Code: "invite_once_secret"}
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

func (s *identityStub) CreateGatewayKey(_ context.Context, _ identity.Principal, userID uuid.UUID, name string, expiresAt *time.Time) (identity.GatewayKey, error) {
	item := identity.GatewayKey{ID: uuid.New(), UserID: userID, Name: name, Prefix: "llmg_test_key", Secret: "llmg_one_time_secret", ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
	s.keys[userID] = append(s.keys[userID], item)
	return item, nil
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

func (s *registryStub) CreateProvider(_ context.Context, _ identity.Principal, provider registry.Provider) (registry.Provider, error) {
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

func (s *registryStub) UpdateProvider(_ context.Context, _ identity.Principal, provider registry.Provider) (registry.Provider, error) {
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

func (s *registryStub) SetProviderEnabled(_ context.Context, _ identity.Principal, providerID uuid.UUID, enabled bool, expectedUpdatedAt time.Time) (registry.Provider, error) {
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

func (s *registryStub) CreateCredential(_ context.Context, _ identity.Principal, input registry.NewCredential, _ string) (registry.Credential, error) {
	item := registry.Credential{ID: uuid.New(), ProviderID: input.ProviderID, Name: input.Name, ResourceDomain: input.ResourceDomain, Status: registry.CredentialActive}
	s.credentials = append(s.credentials, item)
	return item, nil
}

func (s *registryStub) ListCredentials(context.Context, identity.Principal) ([]registry.Credential, error) {
	return append([]registry.Credential(nil), s.credentials...), nil
}

func (s *registryStub) BindCredentialModel(context.Context, identity.Principal, uuid.UUID, uuid.UUID, int32, int32) error {
	return nil
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
	active          configuration.Active
	activeErr       error
	revisions       []configuration.Revision
	publishedID     uuid.UUID
	expectedVersion int64
}

func (s *configurationStub) Active(context.Context, identity.Principal) (configuration.Active, error) {
	return s.active, s.activeErr
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

func (s *configurationStub) Publish(_ context.Context, _ identity.Principal, revisionID uuid.UUID, expectedVersion int64) (configuration.Active, error) {
	s.publishedID = revisionID
	s.expectedVersion = expectedVersion
	for _, revision := range s.revisions {
		if revision.ID == revisionID {
			now := time.Now().UTC()
			revision.PublishedAt = &now
			s.active = configuration.Active{Revision: revision, Version: expectedVersion + 1, UpdatedAt: now}
			s.activeErr = nil
			return s.active, nil
		}
	}
	return configuration.Active{}, configuration.ErrNotFound
}
