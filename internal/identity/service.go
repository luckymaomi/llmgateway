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
	minimumPasswordBytes  = 12
	maximumPasswordBytes  = 1024
	maximumNameRunes      = 80
	credentialPrefixBytes = 13
	defaultSessionLength  = 12 * time.Hour
	maximumKeyModels      = 100
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

func (s *Service) Bootstrap(ctx context.Context, email, displayName, password string) (SessionCredentials, error) {
	newUser, err := s.prepareUser(email, displayName, password, RoleAdministrator, StatusActive)
	if err != nil {
		return SessionCredentials{}, err
	}
	user, err := s.repository.Bootstrap(ctx, newUser)
	if err != nil {
		return SessionCredentials{}, err
	}
	return s.issueSession(ctx, user)
}

func (s *Service) Register(ctx context.Context, invitationCode, email, displayName, password string) (User, error) {
	if !strings.HasPrefix(invitationCode, "invite_") {
		return User{}, ErrInvalidInvitation
	}
	digest, err := security.HMACSHA256(s.sessionPepper, []byte(invitationCode))
	if err != nil {
		return User{}, err
	}
	newUser, err := s.prepareUser(email, displayName, password, RoleMember, StatusPending)
	if err != nil {
		return User{}, err
	}
	return s.repository.Register(ctx, digest[:], newUser)
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
	case StatusPending:
		return SessionCredentials{}, ErrApprovalRequired
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

func (s *Service) CreateInvitation(ctx context.Context, actor Principal, expiresAt time.Time, request MutationRequest) (Invitation, error) {
	if actor.UserID == uuid.Nil || actor.Status != StatusActive || !actor.CanManageUsers() {
		return Invitation{}, ErrForbidden
	}
	normalizedExpiresAt := expiresAt.UTC().Truncate(time.Microsecond)
	mutation, err := newInvitationMutation(request, normalizedExpiresAt)
	if err != nil {
		return Invitation{}, err
	}
	code, err := s.deriveInvitationCode(actor.UserID, request.IdempotencyKey)
	if err != nil {
		return Invitation{}, err
	}
	replayed, found, err := s.repository.ReplayInvitationMutation(ctx, actor.UserID, mutation)
	if err != nil {
		return Invitation{}, err
	}
	if found {
		return attachInvitationCode(replayed, actor.UserID, code)
	}
	now := s.now().UTC()
	if normalizedExpiresAt.Sub(now) < time.Hour || normalizedExpiresAt.Sub(now) > 30*24*time.Hour {
		return Invitation{}, ErrInvalidInput
	}
	digest, err := security.HMACSHA256(s.sessionPepper, []byte(code))
	if err != nil {
		return Invitation{}, err
	}
	invitation, err := s.repository.CreateInvitation(ctx, NewInvitation{
		CodeDigest: digest[:], CodePrefix: credentialPrefix(code), ExpiresAt: normalizedExpiresAt,
	}, actor.UserID, mutation)
	if err != nil {
		return Invitation{}, err
	}
	return attachInvitationCode(invitation, actor.UserID, code)
}

func (s *Service) deriveInvitationCode(actorID, idempotencyKey uuid.UUID) (string, error) {
	material := "llmgateway:invitation-code:" + actorID.String() + ":" + idempotencyKey.String()
	derived, err := security.HMACSHA256(s.sessionPepper, []byte(material))
	if err != nil {
		return "", err
	}
	return "invite_" + base64.RawURLEncoding.EncodeToString(derived[:]), nil
}

func attachInvitationCode(invitation Invitation, actorID uuid.UUID, code string) (Invitation, error) {
	if invitation.ID == uuid.Nil || invitation.CreatedBy != actorID || invitation.Code != "" || invitation.CodePrefix != credentialPrefix(code) {
		return Invitation{}, fmt.Errorf("identity: invalid invitation mutation result")
	}
	invitation.Code = code
	return invitation, nil
}

func (s *Service) CreateGatewayKey(ctx context.Context, actor Principal, userID uuid.UUID, name string, authorizedModelIDs []uuid.UUID, expiresAt *time.Time, request MutationRequest) (GatewayKey, error) {
	if actor.Status != StatusActive || actor.Role != RoleAdministrator {
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
	mutation, err := newGatewayKeyMutation(request, userID, name, normalizedModelIDs, normalizedExpiresAt)
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
		AuthorizedModelIDs: normalizedModelIDs, ExpiresAt: normalizedExpiresAt,
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

func (s *Service) ListUsers(ctx context.Context, actor Principal, status *Status, page Page) (UserPage, error) {
	if !actor.CanManageUsers() {
		return UserPage{}, ErrForbidden
	}
	page = normalizePage(page)
	return s.repository.ListUsers(ctx, status, page)
}

func (s *Service) SetUserStatus(ctx context.Context, actor Principal, userID uuid.UUID, status Status) (User, error) {
	if !actor.CanManageUsers() || actor.UserID == userID {
		return User{}, ErrForbidden
	}
	if status != StatusActive && status != StatusDisabled {
		return User{}, ErrInvalidInput
	}
	return s.repository.SetUserStatus(ctx, userID, status, actor.UserID)
}

func (s *Service) ListInvitations(ctx context.Context, actor Principal, page Page) ([]Invitation, error) {
	if !actor.CanManageUsers() {
		return nil, ErrForbidden
	}
	return s.repository.ListInvitations(ctx, normalizePage(page))
}

func (s *Service) RevokeInvitation(ctx context.Context, actor Principal, invitationID uuid.UUID) error {
	if !actor.CanManageUsers() {
		return ErrForbidden
	}
	return s.repository.RevokeInvitation(ctx, invitationID, actor.UserID)
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
	token, err := security.GenerateToken()
	if err != nil {
		return SessionCredentials{}, err
	}
	csrfToken, err := security.GenerateToken()
	if err != nil {
		return SessionCredentials{}, err
	}
	tokenDigest, err := security.HMACSHA256(s.sessionPepper, []byte(token))
	if err != nil {
		return SessionCredentials{}, err
	}
	csrfDigest, err := security.HMACSHA256(s.sessionPepper, []byte(csrfToken))
	if err != nil {
		return SessionCredentials{}, err
	}
	principal, err := s.repository.CreateSession(ctx, user.ID, tokenDigest[:], csrfDigest[:], s.now().Add(defaultSessionLength))
	if err != nil {
		return SessionCredentials{}, err
	}
	return SessionCredentials{Principal: principal, Token: token, CSRFToken: csrfToken}, nil
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
	if len(password) < minimumPasswordBytes || len(password) > maximumPasswordBytes || strings.ContainsRune(password, '\x00') {
		return NewUser{}, ErrInvalidInput
	}
	passwordHash, err := security.HashPassword(password, s.passwordParams)
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
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidCredential) || errors.Is(err, ErrInvalidInvitation) || errors.Is(err, ErrApprovalRequired) || errors.Is(err, ErrDisabled) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrOutcomeUnknown)
}
