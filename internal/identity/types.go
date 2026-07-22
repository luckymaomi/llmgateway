package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrConflict            = errors.New("identity conflict")
	ErrNotFound            = errors.New("identity record not found")
	ErrInvalidCredential   = errors.New("invalid credential")
	ErrInvalidInvitation   = errors.New("invalid invitation")
	ErrApprovalRequired    = errors.New("account approval required")
	ErrDisabled            = errors.New("account disabled")
	ErrForbidden           = errors.New("operation forbidden")
	ErrInvalidInput        = errors.New("invalid identity input")
	ErrIdempotencyConflict = errors.New("identity idempotency key conflict")
	ErrOutcomeUnknown      = errors.New("identity operation outcome is unknown")
)

type Role string

const (
	RoleAdministrator Role = "administrator"
	RoleMember        Role = "member"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	DisplayName  string     `json:"display_name"`
	Role         Role       `json:"role"`
	Status       Status     `json:"status"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
	DisabledAt   *time.Time `json:"disabled_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	PasswordHash string     `json:"-"`
}

type Principal struct {
	SessionID   uuid.UUID `json:"-"`
	UserID      uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        Role      `json:"role"`
	Status      Status    `json:"status"`
	CSRFDigest  []byte    `json:"-"`
	ExpiresAt   time.Time `json:"-"`
}

func (p Principal) CanManageUsers() bool {
	return p.Role == RoleAdministrator
}

func (p Principal) CanOperateProviders() bool {
	return p.Role == RoleAdministrator
}

type GatewayPrincipal struct {
	KeyID     uuid.UUID
	UserID    uuid.UUID
	Role      Role
	Status    Status
	KeyPrefix string
	ExpiresAt *time.Time
}

type SessionCredentials struct {
	Principal Principal `json:"user"`
	Token     string    `json:"-"`
	CSRFToken string    `json:"csrf_token"`
}

type BootstrapCredentials struct {
	SessionCredentials
	InitialPassword string `json:"-"`
}

type Invitation struct {
	ID         uuid.UUID  `json:"id"`
	CreatedBy  uuid.UUID  `json:"created_by"`
	ClaimedBy  *uuid.UUID `json:"claimed_by,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	CodePrefix string     `json:"code_prefix"`
	Code       string     `json:"code,omitempty"`
}

type GatewayKey struct {
	ID                 uuid.UUID   `json:"id"`
	UserID             uuid.UUID   `json:"user_id"`
	Name               string      `json:"name"`
	Prefix             string      `json:"prefix"`
	Secret             string      `json:"secret,omitempty"`
	AuthorizedModelIDs []uuid.UUID `json:"authorized_model_ids"`
	AuthorizedModels   []string    `json:"authorized_models"`
	ExpiresAt          *time.Time  `json:"expires_at,omitempty"`
	RevokedAt          *time.Time  `json:"revoked_at,omitempty"`
	LastUsedAt         *time.Time  `json:"last_used_at,omitempty"`
	CreatedAt          time.Time   `json:"created_at"`
}

type MutationRequest struct {
	IdempotencyKey uuid.UUID
	RequestID      string
}

type GatewayKeyMutation struct {
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type InvitationMutation struct {
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type NewInvitation struct {
	CodeDigest []byte
	CodePrefix string
	ExpiresAt  time.Time
}

type NewGatewayKey struct {
	UserID             uuid.UUID
	Name               string
	Prefix             string
	SecretDigest       []byte
	AuthorizedModelIDs []uuid.UUID
	ExpiresAt          *time.Time
	ReplacesKeyID      *uuid.UUID
}

type SessionRevocation struct {
	RevokedSessions int64 `json:"revoked_sessions"`
}

type PasswordResetMutation struct {
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type NewUser struct {
	Email        string
	DisplayName  string
	PasswordHash string
	Role         Role
	Status       Status
}

type SessionCreation struct {
	TokenDigest []byte
	CSRFDigest  []byte
	ExpiresAt   time.Time
}

type Page struct {
	Offset int32
	Size   int32
}

type UserPage struct {
	Items []User `json:"items"`
	Total int64  `json:"total"`
}

type Repository interface {
	IsBootstrapped(context.Context) (bool, error)
	Bootstrap(context.Context, NewUser, SessionCreation) (Principal, error)
	Register(context.Context, []byte, NewUser) (User, error)
	FindUserByEmail(context.Context, string) (User, error)
	UserDisplayNames(context.Context, []uuid.UUID) (map[uuid.UUID]string, error)
	ListUsers(context.Context, *Status, Page) (UserPage, error)
	SetUserStatus(context.Context, uuid.UUID, Status, uuid.UUID) (User, error)
	ResetMemberPassword(context.Context, uuid.UUID, string, uuid.UUID, PasswordResetMutation) (SessionRevocation, error)
	ChangeOwnPassword(context.Context, uuid.UUID, uuid.UUID, string, string, string) (SessionRevocation, error)
	RevokeUserSessions(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, string) (SessionRevocation, error)

	CreateSession(context.Context, uuid.UUID, []byte, []byte, time.Time) (Principal, error)
	FindSession(context.Context, []byte) (Principal, error)
	TouchSession(context.Context, uuid.UUID) error
	RevokeSession(context.Context, uuid.UUID) error

	ReplayInvitationMutation(context.Context, uuid.UUID, InvitationMutation) (Invitation, bool, error)
	CreateInvitation(context.Context, NewInvitation, uuid.UUID, InvitationMutation) (Invitation, error)
	ListInvitations(context.Context, Page) ([]Invitation, error)
	RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error

	CreateGatewayKey(context.Context, NewGatewayKey, uuid.UUID, GatewayKeyMutation) (GatewayKey, error)
	GatewayKeyForReplacement(context.Context, uuid.UUID) (GatewayKey, error)
	ListGatewayKeys(context.Context, uuid.UUID) ([]GatewayKey, error)
	RevokeGatewayKey(context.Context, uuid.UUID, uuid.UUID, bool) error
	FindGatewayPrincipal(context.Context, []byte) (GatewayPrincipal, error)
	FindGatewayPrincipalByID(context.Context, uuid.UUID) (GatewayPrincipal, error)
	TouchGatewayKey(context.Context, uuid.UUID) error
}
