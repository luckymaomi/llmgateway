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
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusDeleted  Status = "deleted"
)

type User struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	DisplayName  string     `json:"display_name"`
	Role         Role       `json:"role"`
	Status       Status     `json:"status"`
	DisabledAt   *time.Time `json:"disabled_at,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
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

type MemberMutationAction string

const (
	MemberMutationCreate   MemberMutationAction = "member.create"
	MemberMutationUpdate   MemberMutationAction = "member.update"
	MemberMutationStatus   MemberMutationAction = "member.status"
	MemberMutationDelete   MemberMutationAction = "member.delete"
	MemberMutationPassword MemberMutationAction = "member.password"
)

type MemberMutation struct {
	Action             MemberMutationAction
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type MemberCredentials struct {
	User            User   `json:"user"`
	InitialPassword string `json:"initial_password,omitempty"`
}

type MemberChange struct {
	ID                uuid.UUID
	Email             string
	DisplayName       string
	ExpectedUpdatedAt time.Time
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
	FindUserByEmail(context.Context, string) (User, error)
	UserDisplayNames(context.Context, []uuid.UUID) (map[uuid.UUID]string, error)
	ListUsers(context.Context, *Status, string, Page) (UserPage, error)
	CreateMember(context.Context, NewUser, uuid.UUID, MemberMutation) (User, error)
	UpdateMember(context.Context, MemberChange, uuid.UUID, MemberMutation) (User, error)
	SetUserStatus(context.Context, uuid.UUID, Status, uuid.UUID, MemberMutation) (User, error)
	DeleteMember(context.Context, uuid.UUID, uuid.UUID, MemberMutation) (User, error)
	ResetMemberPassword(context.Context, uuid.UUID, string, uuid.UUID, MemberMutation) (SessionRevocation, error)
	ChangeOwnPassword(context.Context, uuid.UUID, uuid.UUID, string, string, string) (SessionRevocation, error)
	RevokeUserSessions(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, string) (SessionRevocation, error)

	CreateSession(context.Context, uuid.UUID, []byte, []byte, time.Time) (Principal, error)
	FindSession(context.Context, []byte) (Principal, error)
	TouchSession(context.Context, uuid.UUID) error
	RevokeSession(context.Context, uuid.UUID) error

	CreateGatewayKey(context.Context, NewGatewayKey, uuid.UUID, GatewayKeyMutation) (GatewayKey, error)
	GatewayKeyForReplacement(context.Context, uuid.UUID) (GatewayKey, error)
	ListGatewayKeys(context.Context, uuid.UUID) ([]GatewayKey, error)
	RevokeGatewayKey(context.Context, uuid.UUID, uuid.UUID, bool) error
	FindGatewayPrincipal(context.Context, []byte) (GatewayPrincipal, error)
	FindGatewayPrincipalByID(context.Context, uuid.UUID) (GatewayPrincipal, error)
	TouchGatewayKey(context.Context, uuid.UUID) error
}
