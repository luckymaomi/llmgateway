package identity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrConflict          = errors.New("identity conflict")
	ErrNotFound          = errors.New("identity record not found")
	ErrInvalidCredential = errors.New("invalid credential")
	ErrInvalidInvitation = errors.New("invalid invitation")
	ErrApprovalRequired  = errors.New("account approval required")
	ErrDisabled          = errors.New("account disabled")
	ErrForbidden         = errors.New("operation forbidden")
	ErrInvalidInput      = errors.New("invalid identity input")
)

type Role string

const (
	RoleAdministrator Role = "administrator"
	RoleOperator      Role = "operator"
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
	return p.Role == RoleAdministrator || p.Role == RoleOperator
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

type Invitation struct {
	ID         uuid.UUID  `json:"id"`
	Role       Role       `json:"role"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	CodePrefix string     `json:"code_prefix"`
	Code       string     `json:"code,omitempty"`
}

type GatewayKey struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Secret     string     `json:"secret,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type NewUser struct {
	Email        string
	DisplayName  string
	PasswordHash string
	Role         Role
	Status       Status
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
	Bootstrap(context.Context, NewUser) (User, error)
	Register(context.Context, []byte, NewUser) (User, error)
	FindUserByEmail(context.Context, string) (User, error)
	ListUsers(context.Context, *Status, Page) (UserPage, error)
	SetUserStatus(context.Context, uuid.UUID, Status, uuid.UUID) (User, error)

	CreateSession(context.Context, uuid.UUID, []byte, []byte, time.Time) (Principal, error)
	FindSession(context.Context, []byte) (Principal, error)
	TouchSession(context.Context, uuid.UUID) error
	RevokeSession(context.Context, uuid.UUID) error

	CreateInvitation(context.Context, uuid.UUID, []byte, string, Role, time.Time) (Invitation, error)
	ListInvitations(context.Context, Page) ([]Invitation, error)
	RevokeInvitation(context.Context, uuid.UUID, uuid.UUID) error

	CreateGatewayKey(context.Context, uuid.UUID, string, string, []byte, *time.Time, uuid.UUID) (GatewayKey, error)
	ListGatewayKeys(context.Context, uuid.UUID) ([]GatewayKey, error)
	RevokeGatewayKey(context.Context, uuid.UUID, uuid.UUID, bool) error
	FindGatewayPrincipal(context.Context, []byte) (GatewayPrincipal, error)
	TouchGatewayKey(context.Context, uuid.UUID) error
}
