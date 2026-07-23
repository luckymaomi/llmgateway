package identity

import (
	"bytes"
	"context"
	"encoding/base64"
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
	var persistedPasswordHash string
	repository := &repositoryStub{}
	repository.bootstrap = func(_ context.Context, input NewUser, session SessionCreation) (Principal, error) {
		persistedPasswordHash = input.PasswordHash
		if input.Email != "owner@example.com" || input.DisplayName != initialAdministratorDisplayName || input.Role != RoleAdministrator || input.Status != StatusActive {
			t.Fatalf("bootstrap user facts = %+v", input)
		}
		if len(session.TokenDigest) != 32 || len(session.CSRFDigest) != 32 || !session.ExpiresAt.After(time.Now()) {
			t.Fatal("bootstrap transaction did not receive complete session facts")
		}
		return Principal{SessionID: uuid.New(), UserID: userID, Email: input.Email, DisplayName: input.DisplayName, Role: input.Role, Status: input.Status, CSRFDigest: append([]byte(nil), session.CSRFDigest...), ExpiresAt: session.ExpiresAt}, nil
	}

	service := newTestService(t, repository)
	credentials, err := service.Bootstrap(context.Background(), "OWNER@example.com")
	if err != nil {
		t.Fatal(err)
	}
	decodedPassword, err := base64.RawURLEncoding.Strict().DecodeString(credentials.InitialPassword)
	if err != nil || len(decodedPassword) != security.TokenEntropyBytes {
		t.Fatalf("initial password entropy = %d bytes / %v", len(decodedPassword), err)
	}
	passwordVerification, err := security.VerifyPassword(credentials.InitialPassword, persistedPasswordHash, security.RecommendedPasswordParameters())
	if err != nil || !passwordVerification.Match {
		t.Fatalf("initial password did not match persisted Argon2id digest: %+v / %v", passwordVerification, err)
	}
	if credentials.Principal.Role != RoleAdministrator || credentials.Token == "" || credentials.CSRFToken == "" {
		t.Fatalf("bootstrap returned incomplete credentials: %#v", credentials)
	}
	if !service.VerifyCSRF(credentials.Principal, credentials.CSRFToken) {
		t.Fatal("issued CSRF token did not verify")
	}
}

func TestChangePasswordVerifiesCurrentPasswordAndPreservesCurrentSession(t *testing.T) {
	userID, sessionID := uuid.New(), uuid.New()
	currentHash, err := hashPassword("current password value", security.RecommendedPasswordParameters())
	if err != nil {
		t.Fatal(err)
	}
	changeCalls := 0
	repository := &repositoryStub{
		findUserByEmail: func(_ context.Context, email string) (User, error) {
			if email != "owner@example.com" {
				t.Fatalf("password lookup email = %q", email)
			}
			return User{ID: userID, Email: email, PasswordHash: currentHash}, nil
		},
		changeOwnPassword: func(_ context.Context, persistedUserID, persistedSessionID uuid.UUID, expectedHash, replacementHash, requestID string) (SessionRevocation, error) {
			changeCalls++
			if persistedUserID != userID || persistedSessionID != sessionID || expectedHash != currentHash || requestID != "password-change" {
				t.Fatalf("password change identity = %s/%s/%q", persistedUserID, persistedSessionID, requestID)
			}
			verification, verifyErr := security.VerifyPassword("replacement password value", replacementHash, security.RecommendedPasswordParameters())
			if verifyErr != nil || !verification.Match {
				t.Fatalf("replacement password hash = %+v / %v", verification, verifyErr)
			}
			return SessionRevocation{RevokedSessions: 2}, nil
		},
	}
	service := newTestService(t, repository)
	principal := Principal{UserID: userID, SessionID: sessionID, Email: "owner@example.com", Status: StatusActive}
	if _, err := service.ChangePassword(context.Background(), principal, "wrong current password", "replacement password value", "password-change"); err != ErrInvalidCredential {
		t.Fatalf("wrong current password error = %v", err)
	}
	result, err := service.ChangePassword(context.Background(), principal, "current password value", "replacement password value", "password-change")
	if err != nil || result.RevokedSessions != 2 || changeCalls != 1 {
		t.Fatalf("ChangePassword() = %+v, %v; calls=%d", result, err, changeCalls)
	}
}

func TestAdministratorCreatesMemberWithRecoverableInitialPassword(t *testing.T) {
	administratorID, memberID, idempotencyKey := uuid.New(), uuid.New(), uuid.New()
	var passwordHashes []string
	var firstFingerprint []byte
	repository := &repositoryStub{
		createMember: func(_ context.Context, input NewUser, actorID uuid.UUID, mutation MemberMutation) (User, error) {
			if actorID != administratorID || input.Email != "member@example.com" || input.DisplayName != "Member" || input.Role != RoleMember || input.Status != StatusActive {
				t.Fatalf("member creation facts = actor %s input %+v", actorID, input)
			}
			verification, err := security.VerifyPassword("", input.PasswordHash, security.RecommendedPasswordParameters())
			if err != nil || verification.Match {
				t.Fatal("member password was not stored as a one-way hash")
			}
			passwordHashes = append(passwordHashes, input.PasswordHash)
			if firstFingerprint == nil {
				firstFingerprint = append([]byte(nil), mutation.RequestFingerprint...)
			} else if !bytes.Equal(firstFingerprint, mutation.RequestFingerprint) {
				t.Fatal("idempotent member replay changed its request fingerprint")
			}
			return User{ID: memberID, Email: input.Email, DisplayName: input.DisplayName, Role: input.Role, Status: input.Status}, nil
		},
	}
	service := newTestService(t, repository)
	actor := Principal{UserID: administratorID, Role: RoleAdministrator, Status: StatusActive}
	request := MutationRequest{IdempotencyKey: idempotencyKey, RequestID: "member-create"}
	first, err := service.CreateMember(context.Background(), actor, "MEMBER@example.com", " Member ", request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateMember(context.Background(), actor, "member@example.com", "Member", request)
	if err != nil {
		t.Fatal(err)
	}
	if first.User.ID != memberID || first.InitialPassword == "" || second.InitialPassword != first.InitialPassword {
		t.Fatalf("member creation replay = first %+v second %+v", first, second)
	}
	for _, passwordHash := range passwordHashes {
		verification, err := security.VerifyPassword(first.InitialPassword, passwordHash, security.RecommendedPasswordParameters())
		if err != nil || !verification.Match {
			t.Fatal("returned initial password does not match persisted hash")
		}
	}
}

func TestUserDisplayNamesRequiresAnActiveAdministrator(t *testing.T) {
	userID := uuid.New()
	lookupCalls := 0
	repository := &repositoryStub{
		userDisplayNames: func(_ context.Context, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
			lookupCalls++
			if len(userIDs) != 1 || userIDs[0] != userID {
				t.Fatalf("display-name lookup ids = %v, want [%s]", userIDs, userID)
			}
			return map[uuid.UUID]string{userID: "Member"}, nil
		},
	}
	service := newTestService(t, repository)

	names, err := service.UserDisplayNames(context.Background(), Principal{Role: RoleAdministrator, Status: StatusActive}, []uuid.UUID{userID})
	if err != nil {
		t.Fatal(err)
	}
	if names[userID] != "Member" || lookupCalls != 1 {
		t.Fatalf("display-name result/calls = %v/%d", names, lookupCalls)
	}

	for _, actor := range []Principal{
		{Role: RoleMember, Status: StatusActive},
		{Role: RoleAdministrator, Status: StatusDisabled},
	} {
		if _, err := service.UserDisplayNames(context.Background(), actor, []uuid.UUID{userID}); err != ErrForbidden {
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

func TestMemberCreatesOnlyTheirOwnGatewayKey(t *testing.T) {
	memberID := uuid.New()
	persisted := false
	repository := &repositoryStub{
		createGatewayKey: func(_ context.Context, input NewGatewayKey, actorID uuid.UUID, _ GatewayKeyMutation) (GatewayKey, error) {
			if input.UserID != memberID || actorID != memberID {
				t.Fatalf("member key owner = %s, actor = %s", input.UserID, actorID)
			}
			persisted = true
			return GatewayKey{ID: uuid.New(), UserID: memberID}, nil
		},
	}
	service := newTestService(t, repository)
	actor := Principal{UserID: memberID, Role: RoleMember, Status: StatusActive}
	_, err := service.CreateGatewayKey(
		context.Background(),
		actor,
		memberID,
		"Automation",
		[]uuid.UUID{uuid.New()},
		nil,
		MutationRequest{IdempotencyKey: uuid.New(), RequestID: "request-create-key"},
	)
	if err != nil || !persisted {
		t.Fatalf("CreateGatewayKey() error = %v, persisted = %v", err, persisted)
	}
	_, err = service.CreateGatewayKey(
		context.Background(), actor, uuid.New(), "Cross member", []uuid.UUID{uuid.New()}, nil,
		MutationRequest{IdempotencyKey: uuid.New(), RequestID: "request-cross-member-key"},
	)
	if err != ErrForbidden {
		t.Fatalf("cross-member CreateGatewayKey() error = %v, want ErrForbidden", err)
	}
}

func TestAdministratorResetsMemberPasswordWithStablePepperedMutationIdentity(t *testing.T) {
	administratorID, memberID, idempotencyKey := uuid.New(), uuid.New(), uuid.New()
	var firstFingerprint []byte
	calls := 0
	repository := &repositoryStub{
		resetMemberPassword: func(_ context.Context, userID uuid.UUID, passwordHash string, actorID uuid.UUID, mutation MemberMutation) (SessionRevocation, error) {
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
	bootstrap                func(context.Context, NewUser, SessionCreation) (Principal, error)
	findUserByEmail          func(context.Context, string) (User, error)
	userDisplayNames         func(context.Context, []uuid.UUID) (map[uuid.UUID]string, error)
	createMember             func(context.Context, NewUser, uuid.UUID, MemberMutation) (User, error)
	createSession            func(context.Context, uuid.UUID, []byte, []byte, time.Time) (Principal, error)
	createGatewayKey         func(context.Context, NewGatewayKey, uuid.UUID, GatewayKeyMutation) (GatewayKey, error)
	gatewayKeyForReplacement func(context.Context, uuid.UUID) (GatewayKey, error)
	resetMemberPassword      func(context.Context, uuid.UUID, string, uuid.UUID, MemberMutation) (SessionRevocation, error)
	changeOwnPassword        func(context.Context, uuid.UUID, uuid.UUID, string, string, string) (SessionRevocation, error)
	revokeUserSessions       func(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, string) (SessionRevocation, error)
	revokeGatewayKey         func(context.Context, uuid.UUID, uuid.UUID, bool) error
}

func (r *repositoryStub) IsBootstrapped(context.Context) (bool, error) { return false, nil }
func (r *repositoryStub) Bootstrap(ctx context.Context, input NewUser, session SessionCreation) (Principal, error) {
	return r.bootstrap(ctx, input, session)
}
func (r *repositoryStub) FindUserByEmail(ctx context.Context, email string) (User, error) {
	if r.findUserByEmail != nil {
		return r.findUserByEmail(ctx, email)
	}
	return User{}, ErrNotFound
}
func (r *repositoryStub) UserDisplayNames(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	if r.userDisplayNames == nil {
		return map[uuid.UUID]string{}, nil
	}
	return r.userDisplayNames(ctx, userIDs)
}
func (r *repositoryStub) ListUsers(context.Context, *Status, string, Page) (UserPage, error) {
	return UserPage{}, nil
}
func (r *repositoryStub) CreateMember(ctx context.Context, input NewUser, actorID uuid.UUID, mutation MemberMutation) (User, error) {
	return r.createMember(ctx, input, actorID, mutation)
}
func (r *repositoryStub) UpdateMember(context.Context, MemberChange, uuid.UUID, MemberMutation) (User, error) {
	return User{}, nil
}
func (r *repositoryStub) SetUserStatus(context.Context, uuid.UUID, Status, uuid.UUID, MemberMutation) (User, error) {
	return User{}, nil
}
func (r *repositoryStub) DeleteMember(context.Context, uuid.UUID, uuid.UUID, MemberMutation) (User, error) {
	return User{}, nil
}
func (r *repositoryStub) ResetMemberPassword(ctx context.Context, userID uuid.UUID, passwordHash string, actorID uuid.UUID, mutation MemberMutation) (SessionRevocation, error) {
	if r.resetMemberPassword == nil {
		return SessionRevocation{}, nil
	}
	return r.resetMemberPassword(ctx, userID, passwordHash, actorID, mutation)
}
func (r *repositoryStub) ChangeOwnPassword(ctx context.Context, userID, sessionID uuid.UUID, expectedHash, replacementHash, requestID string) (SessionRevocation, error) {
	if r.changeOwnPassword == nil {
		return SessionRevocation{}, nil
	}
	return r.changeOwnPassword(ctx, userID, sessionID, expectedHash, replacementHash, requestID)
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
