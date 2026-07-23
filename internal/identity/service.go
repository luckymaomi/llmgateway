package identity

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/security"
)

const (
	minimumPasswordBytes            = 12
	maximumPasswordBytes            = 1024
	maximumNameRunes                = 80
	credentialPrefixBytes           = 13
	defaultSessionLength            = 12 * time.Hour
	maximumKeyModels                = 100
	initialAdministratorDisplayName = "Administrator"
)

type Service struct {
	repository        Repository
	passwordParams    security.PasswordParameters
	sessionPepper     []byte
	apiKeyPepper      []byte
	now               func() time.Time
	dummyPasswordHash string
}

func NewService(repository Repository, sessionPepper, apiKeyPepper []byte) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("identity repository is required")
	}
	params := security.RecommendedPasswordParameters()
	dummyHash, err := security.HashPassword("not-a-valid-login-password", params)
	if err != nil {
		return nil, fmt.Errorf("initialize password verifier: %w", err)
	}
	if len(sessionPepper) < security.MinimumHMACKeyBytes || len(apiKeyPepper) < security.MinimumHMACKeyBytes {
		return nil, fmt.Errorf("identity peppers must contain at least %d bytes", security.MinimumHMACKeyBytes)
	}
	return &Service{
		repository:        repository,
		passwordParams:    params,
		sessionPepper:     append([]byte(nil), sessionPepper...),
		apiKeyPepper:      append([]byte(nil), apiKeyPepper...),
		now:               func() time.Time { return time.Now().UTC() },
		dummyPasswordHash: dummyHash,
	}, nil
}

func (s *Service) IsBootstrapped(ctx context.Context) (bool, error) {
	return s.repository.IsBootstrapped(ctx)
}

func (s *Service) Bootstrap(ctx context.Context, email string) (BootstrapCredentials, error) {
	initialPassword, err := security.GenerateToken()
	if err != nil {
		return BootstrapCredentials{}, err
	}
	newUser, err := s.prepareUser(email, initialAdministratorDisplayName, initialPassword, RoleAdministrator, StatusActive)
	if err != nil {
		return BootstrapCredentials{}, err
	}
	material, err := s.prepareSession()
	if err != nil {
		return BootstrapCredentials{}, err
	}
	principal, err := s.repository.Bootstrap(ctx, newUser, material.creation)
	if err != nil {
		return BootstrapCredentials{}, err
	}
	return BootstrapCredentials{
		SessionCredentials: SessionCredentials{Principal: principal, Token: material.token, CSRFToken: material.csrfToken},
		InitialPassword:    initialPassword,
	}, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (SessionCredentials, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return SessionCredentials{}, ErrInvalidCredential
	}
	user, lookupErr := s.repository.FindUserByEmail(ctx, normalizedEmail)
	hash := s.dummyPasswordHash
	if lookupErr == nil {
		hash = user.PasswordHash
	}
	verification, verifyErr := security.VerifyPassword(password, hash, s.passwordParams)
	if verifyErr != nil || lookupErr != nil || !verification.Match {
		return SessionCredentials{}, ErrInvalidCredential
	}
	switch user.Status {
	case StatusDisabled:
		return SessionCredentials{}, ErrDisabled
	case StatusActive:
	default:
		return SessionCredentials{}, ErrInvalidCredential
	}
	return s.issueSession(ctx, user)
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (Principal, error) {
	digest, err := security.HMACSHA256(s.sessionPepper, []byte(token))
	if err != nil {
		return Principal{}, ErrInvalidCredential
	}
	principal, err := s.repository.FindSession(ctx, digest[:])
	if err != nil {
		return Principal{}, ErrInvalidCredential
	}
	if principal.Status != StatusActive {
		return Principal{}, ErrDisabled
	}
	_ = s.repository.TouchSession(ctx, principal.SessionID)
	return principal, nil
}

func (s *Service) VerifyCSRF(principal Principal, token string) bool {
	digest, err := security.HMACSHA256(s.sessionPepper, []byte(token))
	if err != nil || len(principal.CSRFDigest) != len(digest) {
		return false
	}
	return subtle.ConstantTimeCompare(digest[:], principal.CSRFDigest) == 1
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	return s.repository.RevokeSession(ctx, principal.SessionID)
}

func (s *Service) ChangePassword(ctx context.Context, principal Principal, currentPassword, replacementPassword, requestID string) (SessionRevocation, error) {
	if principal.UserID == uuid.Nil || principal.SessionID == uuid.Nil || principal.Status != StatusActive || strings.TrimSpace(requestID) == "" {
		return SessionRevocation{}, ErrForbidden
	}
	user, err := s.repository.FindUserByEmail(ctx, principal.Email)
	if err != nil || user.ID != principal.UserID {
		return SessionRevocation{}, ErrInvalidCredential
	}
	verification, err := security.VerifyPassword(currentPassword, user.PasswordHash, s.passwordParams)
	if err != nil || !verification.Match {
		return SessionRevocation{}, ErrInvalidCredential
	}
	replacementMatches, err := security.VerifyPassword(replacementPassword, user.PasswordHash, s.passwordParams)
	if err == nil && replacementMatches.Match {
		return SessionRevocation{}, ErrInvalidInput
	}
	replacementHash, err := hashPassword(replacementPassword, s.passwordParams)
	if err != nil {
		return SessionRevocation{}, err
	}
	return s.repository.ChangeOwnPassword(ctx, principal.UserID, principal.SessionID, user.PasswordHash, replacementHash, requestID)
}

func (s *Service) UserDisplayNames(ctx context.Context, actor Principal, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	if actor.Status != StatusActive || !actor.CanOperateProviders() {
		return nil, ErrForbidden
	}
	uniqueUserIDs := make([]uuid.UUID, 0, len(userIDs))
	seen := make(map[uuid.UUID]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == uuid.Nil {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		uniqueUserIDs = append(uniqueUserIDs, userID)
	}
	return s.repository.UserDisplayNames(ctx, uniqueUserIDs)
}

func (s *Service) CreateGatewayKey(ctx context.Context, actor Principal, userID uuid.UUID, name string, authorizedModelIDs []uuid.UUID, expiresAt *time.Time, request MutationRequest) (GatewayKey, error) {
	if actor.Status != StatusActive || actor.Role != RoleAdministrator && (actor.Role != RoleMember || actor.UserID != userID) {
		return GatewayKey{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	if userID == uuid.Nil || name == "" || utf8.RuneCountInString(name) > maximumNameRunes || len(authorizedModelIDs) == 0 || len(authorizedModelIDs) > maximumKeyModels {
		return GatewayKey{}, ErrInvalidInput
	}
	normalizedModelIDs := append([]uuid.UUID(nil), authorizedModelIDs...)
	sort.Slice(normalizedModelIDs, func(i, j int) bool {
		return normalizedModelIDs[i].String() < normalizedModelIDs[j].String()
	})
	for index, modelID := range normalizedModelIDs {
		if modelID == uuid.Nil || index > 0 && modelID == normalizedModelIDs[index-1] {
			return GatewayKey{}, ErrInvalidInput
		}
	}
	var normalizedExpiresAt *time.Time
	if expiresAt != nil {
		value := expiresAt.UTC()
		normalizedExpiresAt = &value
	}
	if normalizedExpiresAt != nil && !normalizedExpiresAt.After(s.now()) {
		return GatewayKey{}, ErrInvalidInput
	}
	return s.createGatewayKey(ctx, actor, userID, name, normalizedModelIDs, normalizedExpiresAt, nil, request)
}

func (s *Service) ReplaceGatewayKey(ctx context.Context, actor Principal, keyID uuid.UUID, request MutationRequest) (GatewayKey, error) {
	if actor.Status != StatusActive || keyID == uuid.Nil {
		return GatewayKey{}, ErrForbidden
	}
	original, err := s.repository.GatewayKeyForReplacement(ctx, keyID)
	if err != nil {
		return GatewayKey{}, err
	}
	if actor.Role != RoleAdministrator && (actor.Role != RoleMember || actor.UserID != original.UserID) {
		return GatewayKey{}, ErrForbidden
	}
	nameRunes := []rune(original.Name)
	suffix := " replacement"
	if len(nameRunes)+len([]rune(suffix)) > maximumNameRunes {
		nameRunes = nameRunes[:maximumNameRunes-len([]rune(suffix))]
	}
	name := strings.TrimSpace(string(nameRunes)) + suffix
	modelIDs := append([]uuid.UUID(nil), original.AuthorizedModelIDs...)
	sort.Slice(modelIDs, func(i, j int) bool { return modelIDs[i].String() < modelIDs[j].String() })
	return s.createGatewayKey(ctx, actor, original.UserID, name, modelIDs, original.ExpiresAt, &original.ID, request)
}

func (s *Service) createGatewayKey(ctx context.Context, actor Principal, userID uuid.UUID, name string, normalizedModelIDs []uuid.UUID, normalizedExpiresAt *time.Time, replacesKeyID *uuid.UUID, request MutationRequest) (GatewayKey, error) {
	mutation, err := newGatewayKeyMutation(request, userID, name, normalizedModelIDs, normalizedExpiresAt, replacesKeyID)
	if err != nil {
		return GatewayKey{}, err
	}
	secret, err := s.deriveGatewayKeySecret(actor.UserID, request.IdempotencyKey)
	if err != nil {
		return GatewayKey{}, err
	}
	digest, err := security.HMACSHA256(s.apiKeyPepper, []byte(secret))
	if err != nil {
		return GatewayKey{}, err
	}
	key, err := s.repository.CreateGatewayKey(ctx, NewGatewayKey{
		UserID: userID, Name: name, Prefix: credentialPrefix(secret), SecretDigest: digest[:],
		AuthorizedModelIDs: normalizedModelIDs, ExpiresAt: normalizedExpiresAt, ReplacesKeyID: replacesKeyID,
	}, actor.UserID, mutation)
	if err != nil {
		return GatewayKey{}, err
	}
	key.Secret = secret
	return key, nil
}

func (s *Service) deriveGatewayKeySecret(actorID, idempotencyKey uuid.UUID) (string, error) {
	material := "llmgateway:gateway-key-secret:" + actorID.String() + ":" + idempotencyKey.String()
	derived, err := security.HMACSHA256(s.apiKeyPepper, []byte(material))
	if err != nil {
		return "", err
	}
	return "llmg_" + base64.RawURLEncoding.EncodeToString(derived[:]), nil
}

func credentialPrefix(value string) string {
	if len(value) <= credentialPrefixBytes {
		return value
	}
	return value[:credentialPrefixBytes]
}

func (s *Service) AuthenticateGatewayKey(ctx context.Context, secret string) (GatewayPrincipal, error) {
	if !strings.HasPrefix(secret, "llmg_") {
		return GatewayPrincipal{}, ErrInvalidCredential
	}
	digest, err := security.HMACSHA256(s.apiKeyPepper, []byte(secret))
	if err != nil {
		return GatewayPrincipal{}, ErrInvalidCredential
	}
	principal, err := s.repository.FindGatewayPrincipal(ctx, digest[:])
	if err != nil || principal.Status != StatusActive {
		return GatewayPrincipal{}, ErrInvalidCredential
	}
	_ = s.repository.TouchGatewayKey(ctx, principal.KeyID)
	return principal, nil
}

func (s *Service) GatewayPrincipalByID(ctx context.Context, keyID uuid.UUID) (GatewayPrincipal, error) {
	if keyID == uuid.Nil {
		return GatewayPrincipal{}, ErrInvalidCredential
	}
	principal, err := s.repository.FindGatewayPrincipalByID(ctx, keyID)
	if err != nil || principal.Status != StatusActive {
		return GatewayPrincipal{}, ErrInvalidCredential
	}
	return principal, nil
}

func (s *Service) ListUsers(ctx context.Context, actor Principal, status *Status, search string, page Page) (UserPage, error) {
	search = strings.TrimSpace(search)
	if actor.Status != StatusActive || !actor.CanManageUsers() {
		return UserPage{}, ErrForbidden
	}
	if status != nil && *status != StatusActive && *status != StatusDisabled || utf8.RuneCountInString(search) > 200 {
		return UserPage{}, ErrInvalidInput
	}
	page = normalizePage(page)
	return s.repository.ListUsers(ctx, status, search, page)
}

func (s *Service) CreateMember(ctx context.Context, actor Principal, email, displayName string, request MutationRequest) (MemberCredentials, error) {
	if actor.Status != StatusActive || !actor.CanManageUsers() {
		return MemberCredentials{}, ErrForbidden
	}
	password, err := s.deriveMemberPassword(actor.UserID, request.IdempotencyKey)
	if err != nil {
		return MemberCredentials{}, err
	}
	member, err := s.prepareUser(email, displayName, password, RoleMember, StatusActive)
	if err != nil {
		return MemberCredentials{}, err
	}
	mutation, err := newMemberMutation(MemberMutationCreate, request, map[string]any{"email": member.Email, "display_name": member.DisplayName})
	if err != nil {
		return MemberCredentials{}, err
	}
	created, err := s.repository.CreateMember(ctx, member, actor.UserID, mutation)
	if err != nil {
		return MemberCredentials{}, err
	}
	return MemberCredentials{User: created, InitialPassword: password}, nil
}

func (s *Service) deriveMemberPassword(actorID, idempotencyKey uuid.UUID) (string, error) {
	if actorID == uuid.Nil || idempotencyKey == uuid.Nil {
		return "", ErrInvalidInput
	}
	material := "llmgateway:member-initial-password:" + actorID.String() + ":" + idempotencyKey.String()
	derived, err := security.HMACSHA256(s.sessionPepper, []byte(material))
	if err != nil {
		return "", err
	}
	return "Lm!" + base64.RawURLEncoding.EncodeToString(derived[:]), nil
}

func (s *Service) UpdateMember(ctx context.Context, actor Principal, change MemberChange, request MutationRequest) (User, error) {
	if actor.Status != StatusActive || !actor.CanManageUsers() || actor.UserID == change.ID {
		return User{}, ErrForbidden
	}
	email, err := normalizeEmail(change.Email)
	if err != nil {
		return User{}, ErrInvalidInput
	}
	change.Email = email
	change.DisplayName = strings.TrimSpace(change.DisplayName)
	change.ExpectedUpdatedAt = change.ExpectedUpdatedAt.UTC()
	if change.ID == uuid.Nil || change.DisplayName == "" || utf8.RuneCountInString(change.DisplayName) > maximumNameRunes || change.ExpectedUpdatedAt.IsZero() {
		return User{}, ErrInvalidInput
	}
	mutation, err := newMemberMutation(MemberMutationUpdate, request, change)
	if err != nil {
		return User{}, err
	}
	return s.repository.UpdateMember(ctx, change, actor.UserID, mutation)
}

func (s *Service) SetUserStatus(ctx context.Context, actor Principal, userID uuid.UUID, status Status, request MutationRequest) (User, error) {
	if actor.Status != StatusActive || !actor.CanManageUsers() || actor.UserID == userID {
		return User{}, ErrForbidden
	}
	if status != StatusActive && status != StatusDisabled {
		return User{}, ErrInvalidInput
	}
	mutation, err := newMemberMutation(MemberMutationStatus, request, map[string]any{"user_id": userID, "status": status})
	if err != nil {
		return User{}, err
	}
	return s.repository.SetUserStatus(ctx, userID, status, actor.UserID, mutation)
}

func (s *Service) DeleteMember(ctx context.Context, actor Principal, userID uuid.UUID, request MutationRequest) (User, error) {
	if actor.Status != StatusActive || !actor.CanManageUsers() || actor.UserID == userID || userID == uuid.Nil {
		return User{}, ErrForbidden
	}
	mutation, err := newMemberMutation(MemberMutationDelete, request, map[string]any{"user_id": userID})
	if err != nil {
		return User{}, err
	}
	return s.repository.DeleteMember(ctx, userID, actor.UserID, mutation)
}

func (s *Service) ResetMemberPassword(ctx context.Context, actor Principal, userID uuid.UUID, password string, request MutationRequest) (SessionRevocation, error) {
	if actor.Status != StatusActive || !actor.CanManageUsers() || actor.UserID == userID {
		return SessionRevocation{}, ErrForbidden
	}
	if userID == uuid.Nil || request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return SessionRevocation{}, ErrInvalidInput
	}
	passwordHash, err := hashPassword(password, s.passwordParams)
	if err != nil {
		return SessionRevocation{}, err
	}
	fingerprint, err := security.HMACSHA256(s.sessionPepper, []byte("llmgateway:member-password-reset:"+userID.String()+"\x00"+password))
	password = ""
	if err != nil {
		return SessionRevocation{}, err
	}
	mutation := MemberMutation{Action: MemberMutationPassword, IdempotencyKey: request.IdempotencyKey, RequestFingerprint: fingerprint[:], RequestID: request.RequestID}
	return s.repository.ResetMemberPassword(ctx, userID, passwordHash, actor.UserID, mutation)
}

func (s *Service) RevokeUserSessions(ctx context.Context, actor Principal, userID uuid.UUID, requestID string) (SessionRevocation, error) {
	if actor.Status != StatusActive || !actor.CanManageUsers() {
		return SessionRevocation{}, ErrForbidden
	}
	if userID == uuid.Nil || requestID == "" || len(requestID) > 128 {
		return SessionRevocation{}, ErrInvalidInput
	}
	var preservedSessionID *uuid.UUID
	if actor.UserID == userID {
		if actor.SessionID == uuid.Nil {
			return SessionRevocation{}, ErrInvalidInput
		}
		preservedSessionID = &actor.SessionID
	}
	return s.repository.RevokeUserSessions(ctx, userID, actor.UserID, preservedSessionID, requestID)
}

func (s *Service) ListGatewayKeys(ctx context.Context, actor Principal, userID uuid.UUID) ([]GatewayKey, error) {
	if actor.UserID != userID && !actor.CanManageUsers() {
		return nil, ErrForbidden
	}
	return s.repository.ListGatewayKeys(ctx, userID)
}

func (s *Service) RevokeGatewayKey(ctx context.Context, actor Principal, keyID uuid.UUID) error {
	return s.repository.RevokeGatewayKey(ctx, keyID, actor.UserID, actor.CanManageUsers())
}

func (s *Service) issueSession(ctx context.Context, user User) (SessionCredentials, error) {
	material, err := s.prepareSession()
	if err != nil {
		return SessionCredentials{}, err
	}
	principal, err := s.repository.CreateSession(ctx, user.ID, material.creation.TokenDigest, material.creation.CSRFDigest, material.creation.ExpiresAt)
	if err != nil {
		return SessionCredentials{}, err
	}
	return SessionCredentials{Principal: principal, Token: material.token, CSRFToken: material.csrfToken}, nil
}

type sessionMaterial struct {
	token     string
	csrfToken string
	creation  SessionCreation
}

func (s *Service) prepareSession() (sessionMaterial, error) {
	token, err := security.GenerateToken()
	if err != nil {
		return sessionMaterial{}, err
	}
	csrfToken, err := security.GenerateToken()
	if err != nil {
		return sessionMaterial{}, err
	}
	tokenDigest, err := security.HMACSHA256(s.sessionPepper, []byte(token))
	if err != nil {
		return sessionMaterial{}, err
	}
	csrfDigest, err := security.HMACSHA256(s.sessionPepper, []byte(csrfToken))
	if err != nil {
		return sessionMaterial{}, err
	}
	return sessionMaterial{
		token: token, csrfToken: csrfToken,
		creation: SessionCreation{
			TokenDigest: append([]byte(nil), tokenDigest[:]...),
			CSRFDigest:  append([]byte(nil), csrfDigest[:]...),
			ExpiresAt:   s.now().Add(defaultSessionLength),
		},
	}, nil
}

func (s *Service) prepareUser(email, displayName, password string, role Role, status Status) (NewUser, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return NewUser{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if utf8.RuneCountInString(displayName) < 2 || utf8.RuneCountInString(displayName) > maximumNameRunes {
		return NewUser{}, ErrInvalidInput
	}
	passwordHash, err := hashPassword(password, s.passwordParams)
	if err != nil {
		return NewUser{}, fmt.Errorf("hash password: %w", err)
	}
	return NewUser{Email: normalizedEmail, DisplayName: displayName, PasswordHash: passwordHash, Role: role, Status: status}, nil
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 {
		return "", ErrInvalidInput
	}
	return value, nil
}

func normalizePage(page Page) Page {
	if page.Offset < 0 {
		page.Offset = 0
	}
	if page.Size < 1 {
		page.Size = 50
	}
	if page.Size > 200 {
		page.Size = 200
	}
	return page
}

func IsExpectedError(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidCredential) || errors.Is(err, ErrDisabled) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrOutcomeUnknown)
}
