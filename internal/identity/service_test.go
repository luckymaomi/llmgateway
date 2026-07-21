package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/security"
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

func TestInvitationCreationPersistsNoSecretAndReplaysBeforeExpiryValidation(t *testing.T) {
	actorID := uuid.New()
	idempotencyKey := uuid.New()
	now := time.Date(2026, time.July, 20, 7, 0, 0, 123456789, time.UTC)
	expiresAt := now.Add(48 * time.Hour).In(time.FixedZone("request", 8*60*60))
	var persisted Invitation
	var firstMutation InvitationMutation
	createCalls := 0
	repository := &repositoryStub{}
	repository.replayInvitation = func(_ context.Context, persistedActorID uuid.UUID, mutation InvitationMutation) (Invitation, bool, error) {
		if persistedActorID != actorID || mutation.IdempotencyKey != idempotencyKey || len(mutation.RequestFingerprint) != sha256.Size {
			t.Fatal("invitation replay received incomplete mutation identity")
		}
		if persisted.ID == uuid.Nil {
			return Invitation{}, false, nil
		}
		if !bytes.Equal(mutation.RequestFingerprint, firstMutation.RequestFingerprint) {
			t.Fatal("timezone-equivalent invitation replay changed its request fingerprint")
		}
		return persisted, true, nil
	}
	repository.createInvitation = func(_ context.Context, input NewInvitation, persistedActorID uuid.UUID, mutation InvitationMutation) (Invitation, error) {
		createCalls++
		if persistedActorID != actorID || len(input.CodeDigest) != sha256.Size || len(input.CodePrefix) != credentialPrefixBytes {
			t.Fatal("invitation persistence received incomplete non-secret facts")
		}
		if !input.ExpiresAt.Equal(expiresAt.UTC().Truncate(time.Microsecond)) || input.ExpiresAt.Location() != time.UTC {
			t.Fatalf("invitation expiry = %v, want normalized absolute time", input.ExpiresAt)
		}
		firstMutation = mutation
		persisted = Invitation{
			ID: uuid.New(), CreatedBy: actorID, ExpiresAt: input.ExpiresAt,
			CodePrefix: input.CodePrefix, CreatedAt: now,
		}
		return persisted, nil
	}

	service := newTestService(t, repository)
	service.now = func() time.Time { return now }
	request := MutationRequest{IdempotencyKey: idempotencyKey, RequestID: "invitation-create"}
	created, err := service.CreateInvitation(context.Background(), Principal{UserID: actorID, Role: RoleAdministrator, Status: StatusActive}, expiresAt, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Code) <= credentialPrefixBytes || created.Code[:credentialPrefixBytes] != created.CodePrefix || !strings.HasPrefix(created.CodePrefix, "invite_") {
		t.Fatal("invitation creation did not return a valid recoverable code and display prefix")
	}

	service.now = func() time.Time { return now.Add(72 * time.Hour) }
	replayed, err := service.CreateInvitation(
		context.Background(),
		Principal{UserID: actorID, Role: RoleAdministrator, Status: StatusActive},
		expiresAt.UTC(),
		MutationRequest{IdempotencyKey: idempotencyKey, RequestID: "invitation-reconcile"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID || replayed.Code != created.Code || createCalls != 1 {
		t.Fatal("expired invitation mutation replay did not converge on the original entity and code")
	}
}

func TestInvitationCreationRequiresAnActiveAdministrator(t *testing.T) {
	repository := &repositoryStub{
		replayInvitation: func(context.Context, uuid.UUID, InvitationMutation) (Invitation, bool, error) {
			t.Fatal("forbidden invitation creation reached persistence")
			return Invitation{}, false, nil
		},
	}
	service := newTestService(t, repository)
	for _, actor := range []Principal{
		{UserID: uuid.New(), Role: RoleMember, Status: StatusActive},
		{UserID: uuid.New(), Role: RoleAdministrator, Status: StatusDisabled},
	} {
		_, err := service.CreateInvitation(
			context.Background(), actor, time.Now().UTC().Add(24*time.Hour),
			MutationRequest{IdempotencyKey: uuid.New(), RequestID: "invitation-forbidden"},
		)
		if err != ErrForbidden {
			t.Fatalf("CreateInvitation(%s/%s) error = %v, want ErrForbidden", actor.Role, actor.Status, err)
		}
	}
}

func TestInvitationCodeDerivationIsScopedToTheAdministrator(t *testing.T) {
	now := time.Date(2026, time.July, 20, 7, 0, 0, 0, time.UTC)
	idempotencyKey := uuid.New()
	repository := &repositoryStub{
		createInvitation: func(_ context.Context, input NewInvitation, actorID uuid.UUID, _ InvitationMutation) (Invitation, error) {
			return Invitation{
				ID: uuid.New(), CreatedBy: actorID, ExpiresAt: input.ExpiresAt,
				CodePrefix: input.CodePrefix, CreatedAt: now,
			}, nil
		},
	}
	service := newTestService(t, repository)
	service.now = func() time.Time { return now }
	codes := make([]string, 0, 2)
	for _, actorID := range []uuid.UUID{uuid.New(), uuid.New()} {
		created, err := service.CreateInvitation(
			context.Background(),
			Principal{UserID: actorID, Role: RoleAdministrator, Status: StatusActive},
			now.Add(24*time.Hour),
			MutationRequest{IdempotencyKey: idempotencyKey, RequestID: "actor-scoped-invitation"},
		)
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, created.Code)
	}
	if codes[0] == codes[1] {
		t.Fatal("two administrators derived the same invitation code from one idempotency key")
	}
}

func TestUserDisplayNamesRequiresAnActiveAdministratorAndDeduplicatesIDs(t *testing.T) {
	firstUserID, secondUserID := uuid.New(), uuid.New()
	lookupCalls := 0
	repository := &repositoryStub{
		userDisplayNames: func(_ context.Context, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
			lookupCalls++
			if len(userIDs) != 2 || userIDs[0] != firstUserID || userIDs[1] != secondUserID {
				t.Fatalf("display-name lookup ids = %v, want [%s %s]", userIDs, firstUserID, secondUserID)
			}
			return map[uuid.UUID]string{firstUserID: "First User", secondUserID: "Second User"}, nil
		},
	}
	service := newTestService(t, repository)

	names, err := service.UserDisplayNames(context.Background(), Principal{Role: RoleAdministrator, Status: StatusActive}, []uuid.UUID{firstUserID, secondUserID, firstUserID})
	if err != nil {
		t.Fatal(err)
	}
	if names[firstUserID] != "First User" || names[secondUserID] != "Second User" || lookupCalls != 1 {
		t.Fatalf("display-name result/calls = %v/%d", names, lookupCalls)
	}

	for _, actor := range []Principal{
		{Role: RoleMember, Status: StatusActive},
		{Role: RoleAdministrator, Status: StatusDisabled},
	} {
		if _, err := service.UserDisplayNames(context.Background(), actor, []uuid.UUID{firstUserID}); err != ErrForbidden {
			t.Fatalf("UserDisplayNames(%s/%s) error = %v, want ErrForbidden", actor.Role, actor.Status, err)
		}
	}
	if _, err := service.UserDisplayNames(context.Background(), Principal{Role: RoleAdministrator, Status: StatusActive}, []uuid.UUID{uuid.Nil}); err != ErrInvalidInput {
		t.Fatalf("UserDisplayNames(nil id) error = %v, want ErrInvalidInput", err)
	}
	if lookupCalls != 1 {
		t.Fatalf("forbidden or invalid lookup reached persistence; calls = %d", lookupCalls)
	}
}

func TestAdministratorCreatesAnIdempotentGatewayKeyAndMemberRevokesWithinOwnerBoundary(t *testing.T) {
	adminID, userID, keyID := uuid.New(), uuid.New(), uuid.New()
	firstModelID, secondModelID := uuid.New(), uuid.New()
	modelIDs := []uuid.UUID{secondModelID, firstModelID}
	if firstModelID.String() > secondModelID.String() {
		modelIDs = []uuid.UUID{firstModelID, secondModelID}
		firstModelID, secondModelID = secondModelID, firstModelID
	}
	idempotencyKey := uuid.New()
	requestID := "request-create-key"
	expiresAt := time.Now().Add(24 * time.Hour).In(time.FixedZone("test", 8*60*60))
	var firstInput *NewGatewayKey
	var firstMutation *GatewayKeyMutation
	repository := &repositoryStub{}
	repository.createGatewayKey = func(_ context.Context, input NewGatewayKey, actorID uuid.UUID, mutation GatewayKeyMutation) (GatewayKey, error) {
		if actorID != adminID || input.UserID != userID || input.Name != "Automation" || len(input.Prefix) != credentialPrefixBytes || len(input.SecretDigest) != 32 {
			t.Fatal("gateway key persistence received incomplete facts")
		}
		if len(input.AuthorizedModelIDs) != 2 || input.AuthorizedModelIDs[0] != firstModelID || input.AuthorizedModelIDs[1] != secondModelID {
			t.Fatalf("authorized model ids = %v, want sorted ids [%s %s]", input.AuthorizedModelIDs, firstModelID, secondModelID)
		}
		if input.ExpiresAt == nil || input.ExpiresAt.Location() != time.UTC || !input.ExpiresAt.Equal(expiresAt) {
			t.Fatalf("expires at = %v, want UTC %v", input.ExpiresAt, expiresAt.UTC())
		}
		if mutation.IdempotencyKey != idempotencyKey || mutation.RequestID != requestID || len(mutation.RequestFingerprint) != 32 {
			t.Fatalf("gateway key mutation = %#v", mutation)
		}
		if firstInput == nil {
			capturedInput := input
			capturedInput.SecretDigest = append([]byte(nil), input.SecretDigest...)
			capturedInput.AuthorizedModelIDs = append([]uuid.UUID(nil), input.AuthorizedModelIDs...)
			firstInput = &capturedInput
			capturedMutation := mutation
			capturedMutation.RequestFingerprint = append([]byte(nil), mutation.RequestFingerprint...)
			firstMutation = &capturedMutation
		} else if !bytes.Equal(input.SecretDigest, firstInput.SecretDigest) || !bytes.Equal(mutation.RequestFingerprint, firstMutation.RequestFingerprint) {
			t.Fatal("idempotent gateway key replay changed its secret digest or request fingerprint")
		}
		return GatewayKey{ID: keyID, UserID: userID, Name: input.Name, Prefix: input.Prefix, AuthorizedModelIDs: input.AuthorizedModelIDs, ExpiresAt: input.ExpiresAt}, nil
	}
	repository.revokeGatewayKey = func(_ context.Context, persistedKeyID, actorID uuid.UUID, allowAny bool) error {
		if persistedKeyID != keyID || actorID != userID || allowAny {
			t.Fatal("member revocation escaped its ownership boundary")
		}
		return nil
	}

	service := newTestService(t, repository)
	admin := Principal{UserID: adminID, Role: RoleAdministrator, Status: StatusActive}
	request := MutationRequest{IdempotencyKey: idempotencyKey, RequestID: requestID}
	key, err := service.CreateGatewayKey(context.Background(), admin, userID, "Automation", modelIDs, &expiresAt, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(key.Secret) < 32 || key.Secret[:5] != "llmg_" {
		t.Fatal("key creation did not return its recoverable secret")
	}
	replayed, err := service.CreateGatewayKey(context.Background(), admin, userID, "Automation", modelIDs, &expiresAt, request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Secret != key.Secret {
		t.Fatal("idempotent gateway key replay did not return the original secret")
	}
	member := Principal{UserID: userID, Role: RoleMember, Status: StatusActive}
	if err := service.RevokeGatewayKey(context.Background(), member, keyID); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayKeyCreationRequiresAnActiveAdministrator(t *testing.T) {
	repository := &repositoryStub{
		createGatewayKey: func(context.Context, NewGatewayKey, uuid.UUID, GatewayKeyMutation) (GatewayKey, error) {
			t.Fatal("member gateway key creation reached persistence")
			return GatewayKey{}, nil
		},
	}
	service := newTestService(t, repository)
	_, err := service.CreateGatewayKey(
		context.Background(),
		Principal{UserID: uuid.New(), Role: RoleMember, Status: StatusActive},
		uuid.New(),
		"Automation",
		[]uuid.UUID{uuid.New()},
		nil,
		MutationRequest{IdempotencyKey: uuid.New(), RequestID: "request-create-key"},
	)
	if err != ErrForbidden {
		t.Fatalf("CreateGatewayKey() error = %v, want ErrForbidden", err)
	}
}

func TestAdministratorResetsMemberPasswordWithStablePepperedMutationIdentity(t *testing.T) {
	administratorID, memberID, idempotencyKey := uuid.New(), uuid.New(), uuid.New()
	var firstFingerprint []byte
	calls := 0
	repository := &repositoryStub{
		resetMemberPassword: func(_ context.Context, userID uuid.UUID, passwordHash string, actorID uuid.UUID, mutation PasswordResetMutation) (SessionRevocation, error) {
			calls++
			if userID != memberID || actorID != administratorID || mutation.IdempotencyKey != idempotencyKey || mutation.RequestID != "password-reset" {
				t.Fatalf("password reset command = user %s actor %s mutation %+v", userID, actorID, mutation)
			}
			verification, err := security.VerifyPassword("replacement password", passwordHash, security.RecommendedPasswordParameters())
			if err != nil || !verification.Match {
				t.Fatalf("replacement password hash did not verify: %+v / %v", verification, err)
			}
			if len(mutation.RequestFingerprint) != 32 {
				t.Fatalf("password reset fingerprint length = %d", len(mutation.RequestFingerprint))
			}
			if firstFingerprint == nil {
				firstFingerprint = append([]byte(nil), mutation.RequestFingerprint...)
			} else if !bytes.Equal(firstFingerprint, mutation.RequestFingerprint) {
				t.Fatal("same password reset command changed its fingerprint")
			}
			return SessionRevocation{RevokedSessions: 2}, nil
		},
	}
	service := newTestService(t, repository)
	actor := Principal{UserID: administratorID, Role: RoleAdministrator, Status: StatusActive}
	request := MutationRequest{IdempotencyKey: idempotencyKey, RequestID: "password-reset"}
	for range 2 {
		result, err := service.ResetMemberPassword(context.Background(), actor, memberID, "replacement password", request)
		if err != nil || result.RevokedSessions != 2 {
			t.Fatalf("ResetMemberPassword() = %+v, %v", result, err)
		}
	}
	if calls != 2 {
		t.Fatalf("password reset repository calls = %d, want 2", calls)
	}
	if _, err := service.ResetMemberPassword(context.Background(), Principal{UserID: uuid.New(), Role: RoleMember, Status: StatusActive}, memberID, "replacement password", request); err != ErrForbidden {
		t.Fatalf("member ResetMemberPassword() error = %v, want ErrForbidden", err)
	}
}

func TestGatewayKeyReplacementPreservesScopeAndReplaysSecret(t *testing.T) {
	actorID, ownerID, originalID, replacementID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	modelIDs := []uuid.UUID{uuid.New(), uuid.New()}
	sort.Slice(modelIDs, func(i, j int) bool { return modelIDs[i].String() < modelIDs[j].String() })
	lookupModelIDs := []uuid.UUID{modelIDs[1], modelIDs[0]}
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	var firstFingerprint []byte
	repository := &repositoryStub{
		gatewayKeyForReplacement: func(_ context.Context, keyID uuid.UUID) (GatewayKey, error) {
			if keyID != originalID {
				t.Fatalf("replacement lookup key = %s", keyID)
			}
			return GatewayKey{ID: originalID, UserID: ownerID, Name: strings.Repeat("a", maximumNameRunes), AuthorizedModelIDs: lookupModelIDs, ExpiresAt: &expiresAt}, nil
		},
		createGatewayKey: func(_ context.Context, input NewGatewayKey, persistedActorID uuid.UUID, mutation GatewayKeyMutation) (GatewayKey, error) {
			if persistedActorID != actorID || input.ReplacesKeyID == nil || *input.ReplacesKeyID != originalID || input.UserID != ownerID ||
				!slices.Equal(input.AuthorizedModelIDs, modelIDs) || input.ExpiresAt == nil || !input.ExpiresAt.Equal(expiresAt) || utf8.RuneCountInString(input.Name) > maximumNameRunes {
				t.Fatalf("replacement persistence facts = actor %s input %+v", persistedActorID, input)
			}
			if firstFingerprint == nil {
				firstFingerprint = append([]byte(nil), mutation.RequestFingerprint...)
			} else if !bytes.Equal(firstFingerprint, mutation.RequestFingerprint) {
				t.Fatal("replacement replay changed its fingerprint")
			}
			return GatewayKey{ID: replacementID, UserID: ownerID, Name: input.Name, Prefix: input.Prefix, AuthorizedModelIDs: input.AuthorizedModelIDs, ExpiresAt: input.ExpiresAt}, nil
		},
	}
	service := newTestService(t, repository)
	actor := Principal{UserID: actorID, Role: RoleAdministrator, Status: StatusActive}
	request := MutationRequest{IdempotencyKey: uuid.New(), RequestID: "key-replacement"}
	first, err := service.ReplaceGatewayKey(context.Background(), actor, originalID, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ReplaceGatewayKey(context.Background(), actor, originalID, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != replacementID || first.Secret == "" || second.Secret != first.Secret {
		t.Fatalf("replacement replay = first %+v second %+v", first, second)
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
	bootstrap                func(context.Context, NewUser) (User, error)
	register                 func(context.Context, []byte, NewUser) (User, error)
	userDisplayNames         func(context.Context, []uuid.UUID) (map[uuid.UUID]string, error)
	createSession            func(context.Context, uuid.UUID, []byte, []byte, time.Time) (Principal, error)
	replayInvitation         func(context.Context, uuid.UUID, InvitationMutation) (Invitation, bool, error)
	createInvitation         func(context.Context, NewInvitation, uuid.UUID, InvitationMutation) (Invitation, error)
	createGatewayKey         func(context.Context, NewGatewayKey, uuid.UUID, GatewayKeyMutation) (GatewayKey, error)
	gatewayKeyForReplacement func(context.Context, uuid.UUID) (GatewayKey, error)
	resetMemberPassword      func(context.Context, uuid.UUID, string, uuid.UUID, PasswordResetMutation) (SessionRevocation, error)
	revokeUserSessions       func(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, string) (SessionRevocation, error)
	revokeGatewayKey         func(context.Context, uuid.UUID, uuid.UUID, bool) error
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
func (r *repositoryStub) UserDisplayNames(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	if r.userDisplayNames == nil {
		return map[uuid.UUID]string{}, nil
	}
	return r.userDisplayNames(ctx, userIDs)
}
func (r *repositoryStub) ListUsers(context.Context, *Status, Page) (UserPage, error) {
	return UserPage{}, nil
}
func (r *repositoryStub) SetUserStatus(context.Context, uuid.UUID, Status, uuid.UUID) (User, error) {
	return User{}, nil
}
func (r *repositoryStub) ResetMemberPassword(ctx context.Context, userID uuid.UUID, passwordHash string, actorID uuid.UUID, mutation PasswordResetMutation) (SessionRevocation, error) {
	if r.resetMemberPassword == nil {
		return SessionRevocation{}, nil
	}
	return r.resetMemberPassword(ctx, userID, passwordHash, actorID, mutation)
}
func (r *repositoryStub) RevokeUserSessions(ctx context.Context, userID, actorID uuid.UUID, preservedSessionID *uuid.UUID, requestID string) (SessionRevocation, error) {
	if r.revokeUserSessions == nil {
		return SessionRevocation{}, nil
	}
	return r.revokeUserSessions(ctx, userID, actorID, preservedSessionID, requestID)
}
func (r *repositoryStub) CreateSession(ctx context.Context, id uuid.UUID, token, csrf []byte, expires time.Time) (Principal, error) {
	return r.createSession(ctx, id, token, csrf, expires)
}
func (r *repositoryStub) FindSession(context.Context, []byte) (Principal, error) {
	return Principal{}, ErrNotFound
}
func (r *repositoryStub) TouchSession(context.Context, uuid.UUID) error  { return nil }
func (r *repositoryStub) RevokeSession(context.Context, uuid.UUID) error { return nil }
func (r *repositoryStub) ReplayInvitationMutation(ctx context.Context, actorID uuid.UUID, mutation InvitationMutation) (Invitation, bool, error) {
	if r.replayInvitation == nil {
		return Invitation{}, false, nil
	}
	return r.replayInvitation(ctx, actorID, mutation)
}
func (r *repositoryStub) CreateInvitation(ctx context.Context, input NewInvitation, actorID uuid.UUID, mutation InvitationMutation) (Invitation, error) {
	if r.createInvitation == nil {
		return Invitation{}, nil
	}
	return r.createInvitation(ctx, input, actorID, mutation)
}
func (r *repositoryStub) ListInvitations(context.Context, Page) ([]Invitation, error) {
	return []Invitation{}, nil
}
func (r *repositoryStub) RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (r *repositoryStub) CreateGatewayKey(ctx context.Context, input NewGatewayKey, actorID uuid.UUID, mutation GatewayKeyMutation) (GatewayKey, error) {
	return r.createGatewayKey(ctx, input, actorID, mutation)
}
func (r *repositoryStub) GatewayKeyForReplacement(ctx context.Context, keyID uuid.UUID) (GatewayKey, error) {
	if r.gatewayKeyForReplacement == nil {
		return GatewayKey{}, ErrNotFound
	}
	return r.gatewayKeyForReplacement(ctx, keyID)
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
func (r *repositoryStub) FindGatewayPrincipalByID(context.Context, uuid.UUID) (GatewayPrincipal, error) {
	return GatewayPrincipal{}, ErrNotFound
}
func (r *repositoryStub) TouchGatewayKey(context.Context, uuid.UUID) error { return nil }
