package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luckymaomi/llmgateway/internal/identity"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type IdentityRepository struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{pool: pool, queries: db.New(pool)}
}

func (r *IdentityRepository) IsBootstrapped(ctx context.Context) (bool, error) {
	return r.queries.IsBootstrapped(ctx)
}

func (r *IdentityRepository) Bootstrap(ctx context.Context, input identity.NewUser) (identity.User, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return identity.User{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	bootstrapped, err := queries.IsBootstrapped(ctx)
	if err != nil {
		return identity.User{}, err
	}
	if bootstrapped {
		return identity.User{}, identity.ErrConflict
	}
	now := time.Now().UTC()
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: input.Email, DisplayName: input.DisplayName, PasswordHash: input.PasswordHash,
		Role: db.UserRole(input.Role), Status: db.UserStatus(input.Status),
		ApprovedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return identity.User{}, translateStoreError(err)
	}
	rows, err := queries.MarkBootstrapped(ctx)
	if err != nil {
		return identity.User{}, err
	}
	if rows != 1 {
		return identity.User{}, identity.ErrConflict
	}
	if _, err := queries.CreateAuditEvent(ctx, auditParams(&user.ID, "system.bootstrap", "system", "singleton", map[string]any{"administrator_user_id": user.ID})); err != nil {
		return identity.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.User{}, translateStoreError(err)
	}
	return userFromDB(user), nil
}

func (r *IdentityRepository) Register(ctx context.Context, invitationDigest []byte, input identity.NewUser) (identity.User, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return identity.User{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	invitation, err := queries.GetInvitationByDigestForUpdate(ctx, invitationDigest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.User{}, identity.ErrInvalidInvitation
		}
		return identity.User{}, err
	}
	now := time.Now().UTC()
	if invitation.ClaimedAt.Valid || invitation.RevokedAt.Valid || !invitation.ExpiresAt.Time.After(now) {
		return identity.User{}, identity.ErrInvalidInvitation
	}
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: input.Email, DisplayName: input.DisplayName, PasswordHash: input.PasswordHash,
		Role: invitation.Role, Status: db.UserStatusPending,
	})
	if err != nil {
		return identity.User{}, translateStoreError(err)
	}
	rows, err := queries.ClaimInvitation(ctx, db.ClaimInvitationParams{ClaimedBy: &user.ID, ID: invitation.ID})
	if err != nil || rows != 1 {
		if err != nil {
			return identity.User{}, err
		}
		return identity.User{}, identity.ErrInvalidInvitation
	}
	if _, err := queries.CreateAuditEvent(ctx, auditParams(&user.ID, "identity.register", "user", user.ID.String(), map[string]any{"invitation_id": invitation.ID})); err != nil {
		return identity.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.User{}, translateStoreError(err)
	}
	return userFromDB(user), nil
}

func (r *IdentityRepository) FindUserByEmail(ctx context.Context, email string) (identity.User, error) {
	user, err := r.queries.GetUserByEmail(ctx, email)
	if err != nil {
		return identity.User{}, translateStoreError(err)
	}
	return userFromDB(user), nil
}

func (r *IdentityRepository) ListUsers(ctx context.Context, status *identity.Status, page identity.Page) (identity.UserPage, error) {
	var filter *db.UserStatus
	if status != nil {
		value := db.UserStatus(*status)
		filter = &value
	}
	items, err := r.queries.ListUsers(ctx, db.ListUsersParams{Status: filter, PageOffset: page.Offset, PageSize: page.Size})
	if err != nil {
		return identity.UserPage{}, err
	}
	total, err := r.queries.CountUsers(ctx, filter)
	if err != nil {
		return identity.UserPage{}, err
	}
	result := make([]identity.User, 0, len(items))
	for _, item := range items {
		result = append(result, userFromDB(item))
	}
	return identity.UserPage{Items: result, Total: total}, nil
}

func (r *IdentityRepository) SetUserStatus(ctx context.Context, userID uuid.UUID, status identity.Status, actorID uuid.UUID) (identity.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.User{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	user, err := queries.UpdateUserStatus(ctx, db.UpdateUserStatusParams{ID: userID, Status: db.UserStatus(status)})
	if err != nil {
		return identity.User{}, translateStoreError(err)
	}
	if status == identity.StatusDisabled {
		if err := queries.RevokeUserSessions(ctx, userID); err != nil {
			return identity.User{}, err
		}
	}
	if _, err := queries.CreateAuditEvent(ctx, auditParams(&actorID, "identity.status_changed", "user", userID.String(), map[string]any{"status": status})); err != nil {
		return identity.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.User{}, err
	}
	return userFromDB(user), nil
}

func (r *IdentityRepository) CreateSession(ctx context.Context, userID uuid.UUID, tokenDigest, csrfDigest []byte, expiresAt time.Time) (identity.Principal, error) {
	user, err := r.queries.GetUserByID(ctx, userID)
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	session, err := r.queries.CreateSession(ctx, db.CreateSessionParams{UserID: userID, TokenDigest: tokenDigest, CsrfDigest: csrfDigest, ExpiresAt: timestamp(expiresAt)})
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	return identity.Principal{SessionID: session.ID, UserID: user.ID, Email: user.Email, DisplayName: user.DisplayName, Role: identity.Role(user.Role), Status: identity.Status(user.Status), CSRFDigest: session.CsrfDigest, ExpiresAt: session.ExpiresAt.Time}, nil
}

func (r *IdentityRepository) FindSession(ctx context.Context, digest []byte) (identity.Principal, error) {
	session, err := r.queries.GetSessionByDigest(ctx, digest)
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	return identity.Principal{SessionID: session.ID, UserID: session.UserID, Email: session.Email, DisplayName: session.DisplayName, Role: identity.Role(session.Role), Status: identity.Status(session.UserStatus), CSRFDigest: append([]byte(nil), session.CsrfDigest...), ExpiresAt: session.ExpiresAt.Time}, nil
}

func (r *IdentityRepository) TouchSession(ctx context.Context, id uuid.UUID) error {
	return r.queries.TouchSession(ctx, id)
}

func (r *IdentityRepository) RevokeSession(ctx context.Context, id uuid.UUID) error {
	_, err := r.queries.RevokeSession(ctx, id)
	return err
}

func (r *IdentityRepository) CreateInvitation(ctx context.Context, actorID uuid.UUID, digest []byte, codePrefix string, role identity.Role, expiresAt time.Time) (identity.Invitation, error) {
	invitation, err := r.queries.CreateInvitation(ctx, db.CreateInvitationParams{CodeDigest: digest, CodePrefix: codePrefix, CreatedBy: actorID, Role: db.UserRole(role), ExpiresAt: timestamp(expiresAt)})
	if err != nil {
		return identity.Invitation{}, translateStoreError(err)
	}
	return invitationFromDB(invitation), nil
}

func (r *IdentityRepository) ListInvitations(ctx context.Context, page identity.Page) ([]identity.Invitation, error) {
	items, err := r.queries.ListInvitations(ctx, db.ListInvitationsParams{PageOffset: page.Offset, PageSize: page.Size})
	if err != nil {
		return nil, err
	}
	result := make([]identity.Invitation, 0, len(items))
	for _, item := range items {
		result = append(result, invitationFromDB(item))
	}
	return result, nil
}

func (r *IdentityRepository) RevokeInvitation(ctx context.Context, id, actorID uuid.UUID) error {
	rows, err := r.queries.RevokeInvitation(ctx, id)
	if err != nil {
		return err
	}
	if rows == 0 {
		return identity.ErrConflict
	}
	_, err = r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "invitation.revoked", "invitation", id.String(), nil))
	return err
}

func (r *IdentityRepository) CreateGatewayKey(ctx context.Context, userID uuid.UUID, name, prefix string, digest []byte, expiresAt *time.Time, actorID uuid.UUID) (identity.GatewayKey, error) {
	key, err := r.queries.CreateGatewayKey(ctx, db.CreateGatewayKeyParams{UserID: userID, Name: name, Prefix: prefix, SecretDigest: digest, ExpiresAt: optionalTimestamp(expiresAt)})
	if err != nil {
		return identity.GatewayKey{}, translateStoreError(err)
	}
	_, err = r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "gateway_key.created", "gateway_key", key.ID.String(), map[string]any{"user_id": userID, "prefix": prefix}))
	if err != nil {
		return identity.GatewayKey{}, err
	}
	return gatewayKeyFromDB(key.ID, key.UserID, key.Name, key.Prefix, key.ExpiresAt, key.RevokedAt, key.LastUsedAt, key.CreatedAt), nil
}

func (r *IdentityRepository) ListGatewayKeys(ctx context.Context, userID uuid.UUID) ([]identity.GatewayKey, error) {
	items, err := r.queries.ListGatewayKeysByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]identity.GatewayKey, 0, len(items))
	for _, item := range items {
		result = append(result, gatewayKeyFromDB(item.ID, item.UserID, item.Name, item.Prefix, item.ExpiresAt, item.RevokedAt, item.LastUsedAt, item.CreatedAt))
	}
	return result, nil
}

func (r *IdentityRepository) RevokeGatewayKey(ctx context.Context, keyID, actorID uuid.UUID, allowAny bool) error {
	var rows int64
	var err error
	if allowAny {
		rows, err = r.queries.RevokeGatewayKey(ctx, keyID)
	} else {
		rows, err = r.queries.RevokeOwnedGatewayKey(ctx, db.RevokeOwnedGatewayKeyParams{ID: keyID, UserID: actorID})
	}
	if err != nil {
		return err
	}
	if rows == 0 {
		return identity.ErrConflict
	}
	_, err = r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "gateway_key.revoked", "gateway_key", keyID.String(), nil))
	return err
}

func (r *IdentityRepository) FindGatewayPrincipal(ctx context.Context, digest []byte) (identity.GatewayPrincipal, error) {
	key, err := r.queries.GetGatewayKeyByDigest(ctx, digest)
	if err != nil {
		return identity.GatewayPrincipal{}, translateStoreError(err)
	}
	return identity.GatewayPrincipal{KeyID: key.ID, UserID: key.UserID, Role: identity.Role(key.UserRole), Status: identity.Status(key.UserStatus), KeyPrefix: key.Prefix, ExpiresAt: timePointer(key.ExpiresAt)}, nil
}

func (r *IdentityRepository) TouchGatewayKey(ctx context.Context, id uuid.UUID) error {
	return r.queries.TouchGatewayKey(ctx, id)
}

func userFromDB(user db.User) identity.User {
	return identity.User{ID: user.ID, Email: user.Email, DisplayName: user.DisplayName, PasswordHash: user.PasswordHash, Role: identity.Role(user.Role), Status: identity.Status(user.Status), ApprovedAt: timePointer(user.ApprovedAt), DisabledAt: timePointer(user.DisabledAt), CreatedAt: user.CreatedAt.Time, UpdatedAt: user.UpdatedAt.Time}
}

func invitationFromDB(invitation db.Invitation) identity.Invitation {
	return identity.Invitation{ID: invitation.ID, Role: identity.Role(invitation.Role), ExpiresAt: invitation.ExpiresAt.Time, ClaimedAt: timePointer(invitation.ClaimedAt), RevokedAt: timePointer(invitation.RevokedAt), CreatedAt: invitation.CreatedAt.Time, CodePrefix: invitation.CodePrefix}
}

func gatewayKeyFromDB(id, userID uuid.UUID, name, prefix string, expiresAt, revokedAt, lastUsedAt, createdAt pgtype.Timestamptz) identity.GatewayKey {
	return identity.GatewayKey{ID: id, UserID: userID, Name: name, Prefix: prefix, ExpiresAt: timePointer(expiresAt), RevokedAt: timePointer(revokedAt), LastUsedAt: timePointer(lastUsedAt), CreatedAt: createdAt.Time}
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func optionalTimestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return timestamp(*value)
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timeValue := value.Time.UTC()
	return &timeValue
}

func auditParams(actorID *uuid.UUID, action, targetType, targetID string, detail map[string]any) db.CreateAuditEventParams {
	if detail == nil {
		detail = map[string]any{}
	}
	encoded, _ := json.Marshal(detail)
	return db.CreateAuditEventParams{ActorUserID: actorID, Action: action, TargetType: targetType, TargetID: &targetID, Detail: encoded}
}

func translateStoreError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505", "40001":
			return identity.ErrConflict
		}
	}
	return fmt.Errorf("identity store: %w", err)
}
