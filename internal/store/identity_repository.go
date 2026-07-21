package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
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
	pool                       *pgxpool.Pool
	queries                    *db.Queries
	commitInvitationMutation   func(context.Context, pgx.Tx) error
	commitGatewayKeyMutation   func(context.Context, pgx.Tx) error
	commitGatewayKeyRevocation func(context.Context, pgx.Tx) error
}

func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{
		pool: pool, queries: db.New(pool),
		commitInvitationMutation: func(ctx context.Context, tx pgx.Tx) error {
			return tx.Commit(ctx)
		},
		commitGatewayKeyMutation: func(ctx context.Context, tx pgx.Tx) error {
			return tx.Commit(ctx)
		},
		commitGatewayKeyRevocation: func(ctx context.Context, tx pgx.Tx) error {
			return tx.Commit(ctx)
		},
	}
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
		Role: db.UserRoleMember, Status: db.UserStatusPending,
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

func (r *IdentityRepository) UserDisplayNames(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	result := make(map[uuid.UUID]string, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	items, err := r.queries.ListUserDisplayNames(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("identity store: list user display names: %w", err)
	}
	for _, item := range items {
		result[item.ID] = item.DisplayName
	}
	return result, nil
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
		if _, err := queries.RevokeUserSessions(ctx, userID); err != nil {
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

func (r *IdentityRepository) ResetMemberPassword(ctx context.Context, userID uuid.UUID, passwordHash string, actorID uuid.UUID, mutation identity.PasswordResetMutation) (identity.SessionRevocation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimMemberPasswordResetMutation(ctx, db.ClaimMemberPasswordResetMutationParams{
		ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey, UserID: userID,
		RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetMemberPasswordResetMutation(ctx, passwordResetMutationLookup(actorID, mutation))
		if loadErr != nil {
			return identity.SessionRevocation{}, translateStoreError(loadErr)
		}
		return passwordResetMutationResult(existing, userID, mutation)
	}
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	user, err := queries.GetUserForAdministrativeRecovery(ctx, userID)
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	if user.Role != db.UserRoleMember {
		return identity.SessionRevocation{}, identity.ErrForbidden
	}
	if _, err := queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: userID, PasswordHash: passwordHash}); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	revoked, err := queries.RevokeUserSessions(ctx, userID)
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	audit := auditParams(&actorID, "identity.member_password_reset", "user", userID.String(), map[string]any{"revoked_sessions": revoked})
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.SessionRevocation{}, err
	}
	result := identity.SessionRevocation{RevokedSessions: revoked}
	encoded, err := json.Marshal(result)
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	if _, err := queries.CompleteMemberPasswordResetMutation(ctx, db.CompleteMemberPasswordResetMutationParams{ID: operation.ID, Result: encoded}); err != nil {
		return identity.SessionRevocation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.SessionRevocation{}, err
	}
	return result, nil
}

func passwordResetMutationLookup(actorID uuid.UUID, mutation identity.PasswordResetMutation) db.GetMemberPasswordResetMutationParams {
	return db.GetMemberPasswordResetMutationParams{ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey}
}

func passwordResetMutationResult(operation db.MemberPasswordResetMutation, userID uuid.UUID, mutation identity.PasswordResetMutation) (identity.SessionRevocation, error) {
	if operation.UserID != userID || !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return identity.SessionRevocation{}, identity.ErrIdempotencyConflict
	}
	var result identity.SessionRevocation
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		return identity.SessionRevocation{}, fmt.Errorf("identity store: invalid password-reset mutation result")
	}
	return result, nil
}

func (r *IdentityRepository) RevokeUserSessions(ctx context.Context, userID, actorID uuid.UUID, preservedSessionID *uuid.UUID, requestID string) (identity.SessionRevocation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	if _, err := queries.GetUserForAdministrativeRecovery(ctx, userID); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	var revoked int64
	if preservedSessionID == nil {
		revoked, err = queries.RevokeUserSessions(ctx, userID)
	} else {
		revoked, err = queries.RevokeUserSessionsExcept(ctx, db.RevokeUserSessionsExceptParams{UserID: userID, PreservedSessionID: *preservedSessionID})
	}
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	audit := auditParams(&actorID, "identity.sessions_revoked", "user", userID.String(), map[string]any{"revoked_sessions": revoked})
	audit.RequestID = &requestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.SessionRevocation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.SessionRevocation{}, err
	}
	return identity.SessionRevocation{RevokedSessions: revoked}, nil
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

func (r *IdentityRepository) ReplayInvitationMutation(ctx context.Context, actorID uuid.UUID, mutation identity.InvitationMutation) (identity.Invitation, bool, error) {
	operation, err := r.queries.GetInvitationMutation(ctx, invitationMutationLookup(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Invitation{}, false, nil
	}
	if err != nil {
		return identity.Invitation{}, false, translateStoreError(err)
	}
	invitation, err := invitationMutationResult(operation, mutation)
	return invitation, true, err
}

func (r *IdentityRepository) CreateInvitation(ctx context.Context, input identity.NewInvitation, actorID uuid.UUID, mutation identity.InvitationMutation) (identity.Invitation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.Invitation{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimInvitationMutation(ctx, db.ClaimInvitationMutationParams{
		ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey,
		RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetInvitationMutation(ctx, invitationMutationLookup(actorID, mutation))
		if loadErr != nil {
			return identity.Invitation{}, translateStoreError(loadErr)
		}
		return invitationMutationResult(existing, mutation)
	}
	if err != nil {
		return identity.Invitation{}, translateStoreError(err)
	}

	created, err := queries.CreateInvitation(ctx, db.CreateInvitationParams{
		CodeDigest: input.CodeDigest, CodePrefix: input.CodePrefix, CreatedBy: actorID,
		ExpiresAt: timestamp(input.ExpiresAt),
	})
	if err != nil {
		return identity.Invitation{}, translateStoreError(err)
	}
	result := invitationFromDB(created)
	audit := auditParams(&actorID, "invitation.created", "invitation", result.ID.String(), map[string]any{"expires_at": result.ExpiresAt})
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.Invitation{}, err
	}
	encoded, err := json.Marshal(invitationMutationResultFromInvitation(result))
	if err != nil {
		return identity.Invitation{}, fmt.Errorf("encode invitation mutation result: %w", err)
	}
	invitationID := result.ID
	if _, err := queries.CompleteInvitationMutation(ctx, db.CompleteInvitationMutationParams{InvitationID: &invitationID, Result: encoded, ID: operation.ID}); err != nil {
		return identity.Invitation{}, err
	}
	if err := r.commitInvitationMutation(ctx, tx); err != nil {
		return r.reconcileInvitationMutation(ctx, actorID, mutation, err)
	}
	return result, nil
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

func (r *IdentityRepository) CreateGatewayKey(ctx context.Context, input identity.NewGatewayKey, actorID uuid.UUID, mutation identity.GatewayKeyMutation) (identity.GatewayKey, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.GatewayKey{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimGatewayKeyMutation(ctx, db.ClaimGatewayKeyMutationParams{
		ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey,
		RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetGatewayKeyMutation(ctx, gatewayKeyMutationLookup(actorID, mutation))
		if loadErr != nil {
			return identity.GatewayKey{}, translateStoreError(loadErr)
		}
		return gatewayKeyMutationResult(existing, mutation)
	}
	if err != nil {
		return identity.GatewayKey{}, translateStoreError(err)
	}
	if input.ReplacesKeyID != nil {
		original, originalErr := queries.GetGatewayKeyForReplacement(ctx, *input.ReplacesKeyID)
		if originalErr != nil {
			return identity.GatewayKey{}, translateStoreError(originalErr)
		}
		if original.UserID != input.UserID || original.ExpiresAt.Valid != (input.ExpiresAt != nil) ||
			original.ExpiresAt.Valid && !original.ExpiresAt.Time.Equal(input.ExpiresAt.UTC()) {
			return identity.GatewayKey{}, identity.ErrConflict
		}
		bindings, bindingErr := queries.ListGatewayKeyModelBindingsByKey(ctx, *input.ReplacesKeyID)
		if bindingErr != nil {
			return identity.GatewayKey{}, bindingErr
		}
		originalModelIDs := make([]uuid.UUID, 0, len(bindings))
		for _, binding := range bindings {
			originalModelIDs = append(originalModelIDs, binding.ModelID)
		}
		sort.Slice(originalModelIDs, func(i, j int) bool { return originalModelIDs[i].String() < originalModelIDs[j].String() })
		if !slices.Equal(originalModelIDs, input.AuthorizedModelIDs) {
			return identity.GatewayKey{}, identity.ErrConflict
		}
	}

	user, err := queries.GetUserForGatewayKeyCreation(ctx, input.UserID)
	if err != nil {
		return identity.GatewayKey{}, translateStoreError(err)
	}
	if user.Status != db.UserStatusActive {
		return identity.GatewayKey{}, identity.ErrDisabled
	}
	modelNames := make([]string, 0, len(input.AuthorizedModelIDs))
	for _, modelID := range input.AuthorizedModelIDs {
		model, modelErr := queries.GetModelForGatewayKeyBinding(ctx, modelID)
		if modelErr != nil {
			return identity.GatewayKey{}, translateStoreError(modelErr)
		}
		modelNames = append(modelNames, model.PublicName)
	}

	created, err := queries.CreateGatewayKey(ctx, db.CreateGatewayKeyParams{
		UserID: input.UserID, Name: input.Name, Prefix: input.Prefix,
		SecretDigest: input.SecretDigest, ExpiresAt: optionalTimestamp(input.ExpiresAt),
	})
	if err != nil {
		return identity.GatewayKey{}, translateStoreError(err)
	}
	for _, modelID := range input.AuthorizedModelIDs {
		if err := queries.BindGatewayKeyModel(ctx, db.BindGatewayKeyModelParams{GatewayKeyID: created.ID, ModelID: modelID}); err != nil {
			return identity.GatewayKey{}, translateStoreError(err)
		}
	}
	result := gatewayKeyFromDB(created.ID, created.UserID, created.Name, created.Prefix, created.ExpiresAt, created.RevokedAt, created.LastUsedAt, created.CreatedAt)
	result.AuthorizedModelIDs = append([]uuid.UUID(nil), input.AuthorizedModelIDs...)
	result.AuthorizedModels = append([]string(nil), modelNames...)
	action := "gateway_key.created"
	detail := map[string]any{
		"user_id": input.UserID, "prefix": input.Prefix, "model_ids": input.AuthorizedModelIDs, "expires_at": input.ExpiresAt,
	}
	if input.ReplacesKeyID != nil {
		action = "gateway_key.replaced"
		detail["replaces_key_id"] = *input.ReplacesKeyID
	}
	params := auditParams(&actorID, action, "gateway_key", created.ID.String(), detail)
	params.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
		return identity.GatewayKey{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return identity.GatewayKey{}, fmt.Errorf("encode gateway-key mutation result: %w", err)
	}
	keyID := result.ID
	if _, err := queries.CompleteGatewayKeyMutation(ctx, db.CompleteGatewayKeyMutationParams{GatewayKeyID: &keyID, Result: encoded, ID: operation.ID}); err != nil {
		return identity.GatewayKey{}, err
	}
	if err := r.commitGatewayKeyMutation(ctx, tx); err != nil {
		return r.reconcileGatewayKeyMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func (r *IdentityRepository) ListGatewayKeys(ctx context.Context, userID uuid.UUID) ([]identity.GatewayKey, error) {
	items, err := r.queries.ListGatewayKeysByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	bindings, err := r.queries.ListGatewayKeyModelBindingsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	modelIDs := make(map[uuid.UUID][]uuid.UUID)
	modelNames := make(map[uuid.UUID][]string)
	for _, binding := range bindings {
		modelIDs[binding.GatewayKeyID] = append(modelIDs[binding.GatewayKeyID], binding.ModelID)
		modelNames[binding.GatewayKeyID] = append(modelNames[binding.GatewayKeyID], binding.PublicName)
	}
	result := make([]identity.GatewayKey, 0, len(items))
	for _, item := range items {
		key := gatewayKeyFromDB(item.ID, item.UserID, item.Name, item.Prefix, item.ExpiresAt, item.RevokedAt, item.LastUsedAt, item.CreatedAt)
		key.AuthorizedModelIDs = modelIDs[item.ID]
		key.AuthorizedModels = modelNames[item.ID]
		result = append(result, key)
	}
	return result, nil
}

func (r *IdentityRepository) GatewayKeyForReplacement(ctx context.Context, keyID uuid.UUID) (identity.GatewayKey, error) {
	item, err := r.queries.GetGatewayKeyForReplacement(ctx, keyID)
	if err != nil {
		return identity.GatewayKey{}, translateStoreError(err)
	}
	bindings, err := r.queries.ListGatewayKeyModelBindingsByKey(ctx, keyID)
	if err != nil {
		return identity.GatewayKey{}, err
	}
	key := gatewayKeyFromDB(item.ID, item.UserID, item.Name, item.Prefix, item.ExpiresAt, item.RevokedAt, item.LastUsedAt, item.CreatedAt)
	for _, binding := range bindings {
		key.AuthorizedModelIDs = append(key.AuthorizedModelIDs, binding.ModelID)
		key.AuthorizedModels = append(key.AuthorizedModels, binding.PublicName)
	}
	if len(key.AuthorizedModelIDs) == 0 {
		return identity.GatewayKey{}, identity.ErrConflict
	}
	return key, nil
}

func (r *IdentityRepository) reconcileInvitationMutation(ctx context.Context, actorID uuid.UUID, mutation identity.InvitationMutation, commitErr error) (identity.Invitation, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	delay := 20 * time.Millisecond
	var reconciliationErr error
	for {
		operation, err := r.queries.GetInvitationMutation(reconcileCtx, invitationMutationLookup(actorID, mutation))
		if err == nil {
			return invitationMutationResult(operation, mutation)
		}
		reconciliationErr = err
		timer := time.NewTimer(delay)
		select {
		case <-reconcileCtx.Done():
			timer.Stop()
			return identity.Invitation{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", identity.ErrOutcomeUnknown, commitErr, reconciliationErr)
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func invitationMutationLookup(actorID uuid.UUID, mutation identity.InvitationMutation) db.GetInvitationMutationParams {
	return db.GetInvitationMutationParams{ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey}
}

type persistedInvitationMutationResult struct {
	ID         uuid.UUID  `json:"id"`
	CreatedBy  uuid.UUID  `json:"created_by"`
	ClaimedBy  *uuid.UUID `json:"claimed_by,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	CodePrefix string     `json:"code_prefix"`
}

func invitationMutationResultFromInvitation(invitation identity.Invitation) persistedInvitationMutationResult {
	return persistedInvitationMutationResult{
		ID: invitation.ID, CreatedBy: invitation.CreatedBy, ClaimedBy: invitation.ClaimedBy,
		ExpiresAt: invitation.ExpiresAt, ClaimedAt: invitation.ClaimedAt,
		RevokedAt: invitation.RevokedAt, CreatedAt: invitation.CreatedAt, CodePrefix: invitation.CodePrefix,
	}
}

func (result persistedInvitationMutationResult) invitation() identity.Invitation {
	return identity.Invitation{
		ID: result.ID, CreatedBy: result.CreatedBy, ClaimedBy: result.ClaimedBy,
		ExpiresAt: result.ExpiresAt, ClaimedAt: result.ClaimedAt,
		RevokedAt: result.RevokedAt, CreatedAt: result.CreatedAt, CodePrefix: result.CodePrefix,
	}
}

func invitationMutationResult(operation db.InvitationMutation, mutation identity.InvitationMutation) (identity.Invitation, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return identity.Invitation{}, identity.ErrIdempotencyConflict
	}
	var result persistedInvitationMutationResult
	decoder := json.NewDecoder(bytes.NewReader(operation.Result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil ||
		result.ID == uuid.Nil || result.CreatedBy != operation.ActorUserID ||
		operation.InvitationID == nil || *operation.InvitationID != result.ID ||
		len(result.CodePrefix) != 13 || !strings.HasPrefix(result.CodePrefix, "invite_") ||
		result.ExpiresAt.IsZero() || result.CreatedAt.IsZero() {
		return identity.Invitation{}, fmt.Errorf("identity store: invalid invitation mutation result")
	}
	return result.invitation(), nil
}

func (r *IdentityRepository) reconcileGatewayKeyMutation(ctx context.Context, actorID uuid.UUID, mutation identity.GatewayKeyMutation, commitErr error) (identity.GatewayKey, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	delay := 20 * time.Millisecond
	var reconciliationErr error
	for {
		operation, err := r.queries.GetGatewayKeyMutation(reconcileCtx, gatewayKeyMutationLookup(actorID, mutation))
		if err == nil {
			return gatewayKeyMutationResult(operation, mutation)
		}
		reconciliationErr = err
		timer := time.NewTimer(delay)
		select {
		case <-reconcileCtx.Done():
			timer.Stop()
			return identity.GatewayKey{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", identity.ErrOutcomeUnknown, commitErr, reconciliationErr)
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func gatewayKeyMutationLookup(actorID uuid.UUID, mutation identity.GatewayKeyMutation) db.GetGatewayKeyMutationParams {
	return db.GetGatewayKeyMutationParams{ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey}
}

func gatewayKeyMutationResult(operation db.GatewayKeyMutation, mutation identity.GatewayKeyMutation) (identity.GatewayKey, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return identity.GatewayKey{}, identity.ErrIdempotencyConflict
	}
	var result identity.GatewayKey
	if err := json.Unmarshal(operation.Result, &result); err != nil || result.ID == uuid.Nil || operation.GatewayKeyID == nil || *operation.GatewayKeyID != result.ID {
		return identity.GatewayKey{}, fmt.Errorf("identity store: invalid gateway-key mutation result")
	}
	return result, nil
}

func (r *IdentityRepository) RevokeGatewayKey(ctx context.Context, keyID, actorID uuid.UUID, allowAny bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	key, err := queries.GetGatewayKeyForRevocation(ctx, keyID)
	if err != nil {
		return translateStoreError(err)
	}
	if !allowAny && key.UserID != actorID {
		return identity.ErrForbidden
	}
	if key.RevokedAt.Valid {
		return nil
	}
	rows, err := queries.MarkGatewayKeyRevoked(ctx, keyID)
	if err != nil {
		return translateStoreError(err)
	}
	if rows != 1 {
		return fmt.Errorf("identity store: gateway key %s changed while locked", keyID)
	}
	if _, err := queries.CreateAuditEvent(ctx, auditParams(&actorID, "gateway_key.revoked", "gateway_key", keyID.String(), map[string]any{"owner_id": key.UserID})); err != nil {
		return err
	}
	if err := r.commitGatewayKeyRevocation(ctx, tx); err != nil {
		return r.reconcileGatewayKeyRevocation(ctx, keyID, actorID, allowAny, err)
	}
	return nil
}

func (r *IdentityRepository) reconcileGatewayKeyRevocation(ctx context.Context, keyID, actorID uuid.UUID, allowAny bool, commitErr error) error {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	delay := 20 * time.Millisecond
	var reconciliationErr error
	for {
		key, err := r.queries.GetGatewayKeyRevocationState(reconcileCtx, keyID)
		switch {
		case err == nil:
			if !allowAny && key.UserID != actorID {
				return identity.ErrForbidden
			}
			if key.RevokedAt.Valid {
				return nil
			}
			return fmt.Errorf("identity store: revoke gateway key commit: %w", commitErr)
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		default:
			reconciliationErr = err
		}
		timer := time.NewTimer(delay)
		select {
		case <-reconcileCtx.Done():
			timer.Stop()
			return fmt.Errorf("%w: commit: %v; reconciliation: %v", identity.ErrOutcomeUnknown, commitErr, reconciliationErr)
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func (r *IdentityRepository) FindGatewayPrincipal(ctx context.Context, digest []byte) (identity.GatewayPrincipal, error) {
	key, err := r.queries.GetGatewayKeyByDigest(ctx, digest)
	if err != nil {
		return identity.GatewayPrincipal{}, translateStoreError(err)
	}
	return identity.GatewayPrincipal{KeyID: key.ID, UserID: key.UserID, Role: identity.Role(key.UserRole), Status: identity.Status(key.UserStatus), KeyPrefix: key.Prefix, ExpiresAt: timePointer(key.ExpiresAt)}, nil
}

func (r *IdentityRepository) FindGatewayPrincipalByID(ctx context.Context, id uuid.UUID) (identity.GatewayPrincipal, error) {
	key, err := r.queries.GetGatewayKeyPrincipalByID(ctx, id)
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
	return identity.Invitation{ID: invitation.ID, CreatedBy: invitation.CreatedBy, ClaimedBy: invitation.ClaimedBy, ExpiresAt: invitation.ExpiresAt.Time, ClaimedAt: timePointer(invitation.ClaimedAt), RevokedAt: timePointer(invitation.RevokedAt), CreatedAt: invitation.CreatedAt.Time, CodePrefix: invitation.CodePrefix}
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
