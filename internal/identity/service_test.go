package identity

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBootstrapIssuesAuthenticSession(t *testing.T) {
	userID := uuid.New()
	repository := &repositoryStub{}
	repository.bootstrap = func(_ context.Context, input NewUser) (User, error) {
		return User{ID: userID, Email: input.Email, DisplayName: input.DisplayName, Role: input.Role, Status: input.Status, PasswordHash: input.PasswordHash}, nil
	}
	repository.createSession = func(_ context.Context, id uuid.UUID, tokenDigest, csrfDigest []byte, expiresAt time.Time) (Principal, error) {
		if id != userID || len(tokenDigest) != 32 || len(csrfDigest) != 32 || !expiresAt.After(time.Now()) {
			t.Fatal("session persistence did not receive complete facts")
		}
		return Principal{SessionID: uuid.New(), UserID: id, Email: "owner@example.com", DisplayName: "Owner", Role: RoleAdministrator, Status: StatusActive, CSRFDigest: append([]byte(nil), csrfDigest...), ExpiresAt: expiresAt}, nil
	}

	service := newTestService(t, repository)
	credentials, err := service.Bootstrap(context.Background(), "OWNER@example.com", "Owner", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Principal.Role != RoleAdministrator || credentials.Token == "" || credentials.CSRFToken == "" {
		t.Fatalf("bootstrap returned incomplete credentials: %#v", credentials)
	}
	if !service.VerifyCSRF(credentials.Principal, credentials.CSRFToken) {
		t.Fatal("issued CSRF token did not verify")
	}
}

func TestRegisterCreatesPendingUserFromInvitation(t *testing.T) {
	repository := &repositoryStub{}
	repository.register = func(_ context.Context, digest []byte, input NewUser) (User, error) {
		if len(digest) != 32 || input.Status != StatusPending || input.Role != RoleMember {
			t.Fatal("registration did not preserve invitation boundary")
		}
		return User{ID: uuid.New(), Email: input.Email, DisplayName: input.DisplayName, Role: input.Role, Status: input.Status}, nil
	}

	service := newTestService(t, repository)
	user, err := service.Register(context.Background(), "invite_valid-code", "member@example.com", "Member", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if user.Status != StatusPending {
		t.Fatalf("registration status = %s", user.Status)
	}
}

func TestGatewayKeyIsRevealedOnceAndRevokedWithinOwnerBoundary(t *testing.T) {
	userID := uuid.New()
	keyID := uuid.New()
	repository := &repositoryStub{}
	repository.createGatewayKey = func(_ context.Context, persistedUserID uuid.UUID, name, prefix string, digest []byte, _ *time.Time, _ uuid.UUID) (GatewayKey, error) {
		if persistedUserID != userID || name != "Automation" || len(prefix) != 13 || len(digest) != 32 {
			t.Fatal("gateway key persistence received incomplete facts")
		}
		return GatewayKey{ID: keyID, UserID: userID, Name: name, Prefix: prefix}, nil
	}
	repository.revokeGatewayKey = func(_ context.Context, persistedKeyID, actorID uuid.UUID, allowAny bool) error {
		if persistedKeyID != keyID || actorID != userID || allowAny {
			t.Fatal("member revocation escaped its ownership boundary")
		}
		return nil
	}

	service := newTestService(t, repository)
	actor := Principal{UserID: userID, Role: RoleMember, Status: StatusActive}
	key, err := service.CreateGatewayKey(context.Background(), actor, userID, "Automation", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(key.Secret) < 32 || key.Secret[:5] != "llmg_" {
		t.Fatal("key creation did not return the one-time secret")
	}
	if err := service.RevokeGatewayKey(context.Background(), actor, keyID); err != nil {
		t.Fatal(err)
	}
}

func newTestService(t *testing.T, repository Repository) *Service {
	t.Helper()
	service, err := NewService(repository, []byte("test-session-pepper-with-32-bytes"), []byte("test-api-key-pepper-with-32-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type repositoryStub struct {
	bootstrap        func(context.Context, NewUser) (User, error)
	register         func(context.Context, []byte, NewUser) (User, error)
	createSession    func(context.Context, uuid.UUID, []byte, []byte, time.Time) (Principal, error)
	createGatewayKey func(context.Context, uuid.UUID, string, string, []byte, *time.Time, uuid.UUID) (GatewayKey, error)
	revokeGatewayKey func(context.Context, uuid.UUID, uuid.UUID, bool) error
}

func (r *repositoryStub) IsBootstrapped(context.Context) (bool, error) { return false, nil }
func (r *repositoryStub) Bootstrap(ctx context.Context, input NewUser) (User, error) {
	return r.bootstrap(ctx, input)
}
func (r *repositoryStub) Register(ctx context.Context, digest []byte, input NewUser) (User, error) {
	return r.register(ctx, digest, input)
}
func (r *repositoryStub) FindUserByEmail(context.Context, string) (User, error) {
	return User{}, ErrNotFound
}
func (r *repositoryStub) ListUsers(context.Context, *Status, Page) (UserPage, error) {
	return UserPage{}, nil
}
func (r *repositoryStub) SetUserStatus(context.Context, uuid.UUID, Status, uuid.UUID) (User, error) {
	return User{}, nil
}
func (r *repositoryStub) CreateSession(ctx context.Context, id uuid.UUID, token, csrf []byte, expires time.Time) (Principal, error) {
	return r.createSession(ctx, id, token, csrf, expires)
}
func (r *repositoryStub) FindSession(context.Context, []byte) (Principal, error) {
	return Principal{}, ErrNotFound
}
func (r *repositoryStub) TouchSession(context.Context, uuid.UUID) error  { return nil }
func (r *repositoryStub) RevokeSession(context.Context, uuid.UUID) error { return nil }
func (r *repositoryStub) CreateInvitation(context.Context, uuid.UUID, []byte, Role, time.Time) (Invitation, error) {
	return Invitation{}, nil
}
func (r *repositoryStub) ListInvitations(context.Context, Page) ([]Invitation, error) {
	return []Invitation{}, nil
}
func (r *repositoryStub) RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (r *repositoryStub) CreateGatewayKey(ctx context.Context, userID uuid.UUID, name, prefix string, digest []byte, expires *time.Time, actorID uuid.UUID) (GatewayKey, error) {
	return r.createGatewayKey(ctx, userID, name, prefix, digest, expires, actorID)
}
func (r *repositoryStub) ListGatewayKeys(context.Context, uuid.UUID) ([]GatewayKey, error) {
	return []GatewayKey{}, nil
}
func (r *repositoryStub) RevokeGatewayKey(ctx context.Context, keyID, actorID uuid.UUID, allowAny bool) error {
	return r.revokeGatewayKey(ctx, keyID, actorID, allowAny)
}
func (r *repositoryStub) FindGatewayPrincipal(context.Context, []byte) (GatewayPrincipal, error) {
	return GatewayPrincipal{}, ErrNotFound
}
func (r *repositoryStub) TouchGatewayKey(context.Context, uuid.UUID) error { return nil }
