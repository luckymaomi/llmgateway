package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
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
	commitMemberMutation       func(context.Context, pgx.Tx) error
	commitGatewayKeyMutation   func(context.Context, pgx.Tx) error
	commitGatewayKeyRevocation func(context.Context, pgx.Tx) error
}

func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{
		pool: pool, queries: db.New(pool),
		commitMemberMutation:       func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
		commitGatewayKeyMutation:   func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
		commitGatewayKeyRevocation: func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
	}
}

func (r *IdentityRepository) IsBootstrapped(ctx context.Context) (bool, error) {
	return r.queries.IsBootstrapped(ctx)
}

func (r *IdentityRepository) Bootstrap(ctx context.Context, input identity.NewUser, session identity.SessionCreation) (identity.Principal, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return identity.Principal{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	bootstrapped, err := queries.IsBootstrapped(ctx)
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	if bootstrapped {
		return identity.Principal{}, identity.ErrConflict
	}
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: input.Email, DisplayName: input.DisplayName, PasswordHash: input.PasswordHash,
		Role: db.UserRole(input.Role), Status: db.UserStatus(input.Status),
	})
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	rows, err := queries.MarkBootstrapped(ctx)
	if err != nil || rows != 1 {
		return identity.Principal{}, identity.ErrConflict
	}
	if _, err := queries.CreateAuditEvent(ctx, auditParams(&user.ID, "system.bootstrap", "system", "singleton", map[string]any{"administrator_user_id": user.ID})); err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	createdSession, err := queries.CreateSession(ctx, db.CreateSessionParams{
		UserID: user.ID, TokenDigest: session.TokenDigest, CsrfDigest: session.CSRFDigest, ExpiresAt: timestamp(session.ExpiresAt),
	})
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	return identity.Principal{
		SessionID: createdSession.ID, UserID: user.ID, Email: user.Email, DisplayName: user.DisplayName,
		Role: identity.Role(user.Role), Status: identity.Status(user.Status),
		CSRFDigest: append([]byte(nil), createdSession.CsrfDigest...), ExpiresAt: createdSession.ExpiresAt.Time.UTC(),
	}, nil
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
		return nil, err
	}
	for _, item := range items {
		result[item.ID] = item.DisplayName
	}
	return result, nil
}

func (r *IdentityRepository) ListUsers(ctx context.Context, status *identity.Status, search string, page identity.Page) (identity.UserPage, error) {
	var databaseStatus *db.UserStatus
	if status != nil {
		value := db.UserStatus(*status)
		databaseStatus = &value
	}
	params := db.ListUsersParams{Status: databaseStatus, Search: search, PageSize: page.Size, PageOffset: page.Offset}
	items, err := r.queries.ListUsers(ctx, params)
	if err != nil {
		return identity.UserPage{}, translateStoreError(err)
	}
	total, err := r.queries.CountUsers(ctx, db.CountUsersParams{Status: databaseStatus, Search: search})
	if err != nil {
		return identity.UserPage{}, translateStoreError(err)
	}
	result := make([]identity.User, 0, len(items))
	for _, item := range items {
		result = append(result, userFromDB(item))
	}
	return identity.UserPage{Items: result, Total: total}, nil
}

func (r *IdentityRepository) CreateMember(ctx context.Context, input identity.NewUser, actorID uuid.UUID, mutation identity.MemberMutation) (identity.User, error) {
	return r.mutateMember(ctx, actorID, nil, mutation, func(queries *db.Queries) (identity.User, string, map[string]any, error) {
		created, err := queries.CreateUser(ctx, db.CreateUserParams{
			Email: input.Email, DisplayName: input.DisplayName, PasswordHash: input.PasswordHash,
			Role: db.UserRoleMember, Status: db.UserStatusActive,
		})
		if err != nil {
			return identity.User{}, "", nil, translateStoreError(err)
		}
		return userFromDB(created), "identity.member_created", map[string]any{"email": created.Email}, nil
	})
}

func (r *IdentityRepository) UpdateMember(ctx context.Context, change identity.MemberChange, actorID uuid.UUID, mutation identity.MemberMutation) (identity.User, error) {
	return r.mutateMember(ctx, actorID, &change.ID, mutation, func(queries *db.Queries) (identity.User, string, map[string]any, error) {
		updated, err := queries.UpdateUserProfile(ctx, db.UpdateUserProfileParams{
			Email: change.Email, DisplayName: change.DisplayName, ID: change.ID, ExpectedUpdatedAt: timestamp(change.ExpectedUpdatedAt),
		})
		if err != nil {
			return identity.User{}, "", nil, translateStoreError(err)
		}
		return userFromDB(updated), "identity.member_updated", map[string]any{"email": updated.Email}, nil
	})
}

func (r *IdentityRepository) SetUserStatus(ctx context.Context, userID uuid.UUID, status identity.Status, actorID uuid.UUID, mutation identity.MemberMutation) (identity.User, error) {
	return r.mutateMember(ctx, actorID, &userID, mutation, func(queries *db.Queries) (identity.User, string, map[string]any, error) {
		updated, err := queries.UpdateUserStatus(ctx, db.UpdateUserStatusParams{Status: db.UserStatus(status), ID: userID})
		if err != nil {
			return identity.User{}, "", nil, translateStoreError(err)
		}
		var revoked int64
		if status == identity.StatusDisabled {
			revoked, err = queries.RevokeUserSessions(ctx, userID)
			if err != nil {
				return identity.User{}, "", nil, translateStoreError(err)
			}
		}
		return userFromDB(updated), "identity.member_status_changed", map[string]any{"status": status, "revoked_sessions": revoked}, nil
	})
}

func (r *IdentityRepository) DeleteMember(ctx context.Context, userID, actorID uuid.UUID, mutation identity.MemberMutation) (identity.User, error) {
	return r.mutateMember(ctx, actorID, &userID, mutation, func(queries *db.Queries) (identity.User, string, map[string]any, error) {
		updated, err := queries.MarkUserDeleted(ctx, userID)
		if err != nil {
			return identity.User{}, "", nil, translateStoreError(err)
		}
		revokedSessions, err := queries.RevokeUserSessions(ctx, userID)
		if err != nil {
			return identity.User{}, "", nil, translateStoreError(err)
		}
		revokedKeys, err := queries.RevokeGatewayKeysForUser(ctx, userID)
		if err != nil {
			return identity.User{}, "", nil, translateStoreError(err)
		}
		return userFromDB(updated), "identity.member_deleted", map[string]any{"revoked_sessions": revokedSessions, "revoked_api_keys": revokedKeys}, nil
	})
}

type memberMutationApply func(*db.Queries) (identity.User, string, map[string]any, error)

func (r *IdentityRepository) mutateMember(ctx context.Context, actorID uuid.UUID, userID *uuid.UUID, mutation identity.MemberMutation, apply memberMutationApply) (identity.User, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return identity.User{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimMemberMutation(ctx, db.ClaimMemberMutationParams{
		ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey,
		UserID: userID, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetMemberMutation(ctx, memberMutationLookup(actorID, mutation))
		if loadErr != nil {
			return identity.User{}, translateStoreError(loadErr)
		}
		return memberMutationUserResult(existing, mutation)
	}
	if err != nil {
		return identity.User{}, translateStoreError(err)
	}
	result, auditAction, detail, err := apply(queries)
	if err != nil {
		return identity.User{}, err
	}
	audit := auditParams(&actorID, auditAction, "user", result.ID.String(), detail)
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.User{}, translateStoreError(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return identity.User{}, err
	}
	if _, err := queries.CompleteMemberMutation(ctx, db.CompleteMemberMutationParams{UserID: &result.ID, Result: encoded, ID: operation.ID}); err != nil {
		return identity.User{}, translateStoreError(err)
	}
	if err := r.commitMemberMutation(ctx, tx); err != nil {
		return r.reconcileMemberUserMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func (r *IdentityRepository) ResetMemberPassword(ctx context.Context, userID uuid.UUID, passwordHash string, actorID uuid.UUID, mutation identity.MemberMutation) (identity.SessionRevocation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimMemberMutation(ctx, db.ClaimMemberMutationParams{
		ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey,
		UserID: &userID, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetMemberMutation(ctx, memberMutationLookup(actorID, mutation))
		if loadErr != nil {
			return identity.SessionRevocation{}, translateStoreError(loadErr)
		}
		return memberMutationRevocationResult(existing, userID, mutation)
	}
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	user, err := queries.GetUserForAdministrativeRecovery(ctx, userID)
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	if user.Role != db.UserRoleMember || user.Status == db.UserStatusDeleted {
		return identity.SessionRevocation{}, identity.ErrForbidden
	}
	if _, err := queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: passwordHash, ID: userID}); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	revoked, err := queries.RevokeUserSessions(ctx, userID)
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	result := identity.SessionRevocation{RevokedSessions: revoked}
	audit := auditParams(&actorID, "identity.member_password_reset", "user", userID.String(), map[string]any{"revoked_sessions": revoked})
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	if _, err := queries.CompleteMemberMutation(ctx, db.CompleteMemberMutationParams{UserID: &userID, Result: encoded, ID: operation.ID}); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	if err := r.commitMemberMutation(ctx, tx); err != nil {
		return r.reconcileMemberRevocationMutation(ctx, actorID, userID, mutation, err)
	}
	return result, nil
}

func (r *IdentityRepository) ChangeOwnPassword(ctx context.Context, userID, sessionID uuid.UUID, expectedPasswordHash, replacementPasswordHash, requestID string) (identity.SessionRevocation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	rows, err := queries.UpdateOwnPassword(ctx, db.UpdateOwnPasswordParams{ReplacementPasswordHash: replacementPasswordHash, ID: userID, ExpectedPasswordHash: expectedPasswordHash})
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	if rows != 1 {
		return identity.SessionRevocation{}, identity.ErrInvalidCredential
	}
	revoked, err := queries.RevokeUserSessionsExcept(ctx, db.RevokeUserSessionsExceptParams{UserID: userID, PreservedSessionID: sessionID})
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	audit := auditParams(&userID, "identity.password_changed", "user", userID.String(), map[string]any{"revoked_sessions": revoked})
	audit.RequestID = &requestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	return identity.SessionRevocation{RevokedSessions: revoked}, nil
}

func (r *IdentityRepository) RevokeUserSessions(ctx context.Context, userID, actorID uuid.UUID, preservedSessionID *uuid.UUID, requestID string) (identity.SessionRevocation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.SessionRevocation{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	var revoked int64
	if preservedSessionID == nil {
		revoked, err = queries.RevokeUserSessions(ctx, userID)
	} else {
		revoked, err = queries.RevokeUserSessionsExcept(ctx, db.RevokeUserSessionsExceptParams{UserID: userID, PreservedSessionID: *preservedSessionID})
	}
	if err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	audit := auditParams(&actorID, "identity.sessions_revoked", "user", userID.String(), map[string]any{"revoked_sessions": revoked})
	audit.RequestID = &requestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.SessionRevocation{}, translateStoreError(err)
	}
	return identity.SessionRevocation{RevokedSessions: revoked}, nil
}

func (r *IdentityRepository) CreateSession(ctx context.Context, userID uuid.UUID, tokenDigest, csrfDigest []byte, expiresAt time.Time) (identity.Principal, error) {
	user, err := r.queries.GetUserByID(ctx, userID)
	if err != nil || user.Status != db.UserStatusActive {
		return identity.Principal{}, identity.ErrDisabled
	}
	session, err := r.queries.CreateSession(ctx, db.CreateSessionParams{UserID: userID, TokenDigest: tokenDigest, CsrfDigest: csrfDigest, ExpiresAt: timestamp(expiresAt)})
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	return identity.Principal{SessionID: session.ID, UserID: user.ID, Email: user.Email, DisplayName: user.DisplayName, Role: identity.Role(user.Role), Status: identity.Status(user.Status), CSRFDigest: append([]byte(nil), session.CsrfDigest...), ExpiresAt: session.ExpiresAt.Time.UTC()}, nil
}

func (r *IdentityRepository) FindSession(ctx context.Context, digest []byte) (identity.Principal, error) {
	session, err := r.queries.GetSessionByDigest(ctx, digest)
	if err != nil {
		return identity.Principal{}, translateStoreError(err)
	}
	return identity.Principal{SessionID: session.ID, UserID: session.UserID, Email: session.Email, DisplayName: session.DisplayName, Role: identity.Role(session.Role), Status: identity.Status(session.UserStatus), CSRFDigest: append([]byte(nil), session.CsrfDigest...), ExpiresAt: session.ExpiresAt.Time.UTC()}, nil
}

func (r *IdentityRepository) TouchSession(ctx context.Context, id uuid.UUID) error {
	return r.queries.TouchSession(ctx, id)
}

func (r *IdentityRepository) RevokeSession(ctx context.Context, id uuid.UUID) error {
	_, err := r.queries.RevokeSession(ctx, id)
	return err
}

func (r *IdentityRepository) CreateGatewayKey(ctx context.Context, input identity.NewGatewayKey, actorID uuid.UUID, mutation identity.GatewayKeyMutation) (identity.GatewayKey, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.GatewayKey{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimGatewayKeyMutation(ctx, db.ClaimGatewayKeyMutationParams{ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID})
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
		if original.UserID != input.UserID || original.ExpiresAt.Valid != (input.ExpiresAt != nil) || original.ExpiresAt.Valid && !original.ExpiresAt.Time.Equal(input.ExpiresAt.UTC()) {
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
		model, modelErr := queries.GetModelForGatewayKeyBinding(ctx, db.GetModelForGatewayKeyBindingParams{ID: modelID, UserID: input.UserID})
		if modelErr != nil {
			return identity.GatewayKey{}, translateStoreError(modelErr)
		}
		modelNames = append(modelNames, model.PublicName)
	}
	created, err := queries.CreateGatewayKey(ctx, db.CreateGatewayKeyParams{UserID: input.UserID, Name: input.Name, Prefix: input.Prefix, SecretDigest: input.SecretDigest, ExpiresAt: optionalTimestamp(input.ExpiresAt)})
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
	detail := map[string]any{"user_id": input.UserID, "prefix": input.Prefix, "model_ids": input.AuthorizedModelIDs, "expires_at": input.ExpiresAt}
	if input.ReplacesKeyID != nil {
		action = "gateway_key.replaced"
		detail["replaces_key_id"] = *input.ReplacesKeyID
	}
	audit := auditParams(&actorID, action, "gateway_key", created.ID.String(), detail)
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return identity.GatewayKey{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return identity.GatewayKey{}, err
	}
	if _, err := queries.CompleteGatewayKeyMutation(ctx, db.CompleteGatewayKeyMutationParams{GatewayKeyID: &result.ID, Result: encoded, ID: operation.ID}); err != nil {
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
		key.AuthorizedModelIDs, key.AuthorizedModels = modelIDs[item.ID], modelNames[item.ID]
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
	if key.UserID != actorID && !allowAny {
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
		return identity.ErrConflict
	}
	if _, err := queries.CreateAuditEvent(ctx, auditParams(&actorID, "gateway_key.revoked", "gateway_key", keyID.String(), map[string]any{"owner_id": key.UserID})); err != nil {
		return err
	}
	if err := r.commitGatewayKeyRevocation(ctx, tx); err != nil {
		return r.reconcileGatewayKeyRevocation(ctx, keyID, actorID, allowAny, err)
	}
	return nil
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

func memberMutationLookup(actorID uuid.UUID, mutation identity.MemberMutation) db.GetMemberMutationParams {
	return db.GetMemberMutationParams{ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey}
}

func memberMutationUserResult(operation db.MemberMutation, mutation identity.MemberMutation) (identity.User, error) {
	if operation.Action != string(mutation.Action) || !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return identity.User{}, identity.ErrIdempotencyConflict
	}
	var result identity.User
	if err := json.Unmarshal(operation.Result, &result); err != nil || result.ID == uuid.Nil || operation.UserID == nil || *operation.UserID != result.ID {
		return identity.User{}, fmt.Errorf("identity store: invalid member mutation result")
	}
	return result, nil
}

func memberMutationRevocationResult(operation db.MemberMutation, userID uuid.UUID, mutation identity.MemberMutation) (identity.SessionRevocation, error) {
	if operation.Action != string(mutation.Action) || !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) || operation.UserID == nil || *operation.UserID != userID {
		return identity.SessionRevocation{}, identity.ErrIdempotencyConflict
	}
	var result identity.SessionRevocation
	if err := json.Unmarshal(operation.Result, &result); err != nil {
		return identity.SessionRevocation{}, fmt.Errorf("identity store: invalid member password mutation result")
	}
	return result, nil
}

func (r *IdentityRepository) reconcileMemberUserMutation(ctx context.Context, actorID uuid.UUID, mutation identity.MemberMutation, commitErr error) (identity.User, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetMemberMutation(reconcileCtx, memberMutationLookup(actorID, mutation))
		if err == nil {
			return memberMutationUserResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return identity.User{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", identity.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func (r *IdentityRepository) reconcileMemberRevocationMutation(ctx context.Context, actorID, userID uuid.UUID, mutation identity.MemberMutation, commitErr error) (identity.SessionRevocation, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetMemberMutation(reconcileCtx, memberMutationLookup(actorID, mutation))
		if err == nil {
			return memberMutationRevocationResult(operation, userID, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return identity.SessionRevocation{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", identity.ErrOutcomeUnknown, commitErr, err)
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

func (r *IdentityRepository) reconcileGatewayKeyMutation(ctx context.Context, actorID uuid.UUID, mutation identity.GatewayKeyMutation, commitErr error) (identity.GatewayKey, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetGatewayKeyMutation(reconcileCtx, gatewayKeyMutationLookup(actorID, mutation))
		if err == nil {
			return gatewayKeyMutationResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return identity.GatewayKey{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", identity.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func (r *IdentityRepository) reconcileGatewayKeyRevocation(ctx context.Context, keyID, actorID uuid.UUID, allowAny bool, commitErr error) error {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		state, err := r.queries.GetGatewayKeyRevocationState(reconcileCtx, keyID)
		if err == nil {
			if state.UserID != actorID && !allowAny {
				return identity.ErrForbidden
			}
			if state.RevokedAt.Valid {
				return nil
			}
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return fmt.Errorf("%w: commit: %v; reconciliation: %v", identity.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func userFromDB(user db.User) identity.User {
	return identity.User{ID: user.ID, Email: user.Email, DisplayName: user.DisplayName, PasswordHash: user.PasswordHash, Role: identity.Role(user.Role), Status: identity.Status(user.Status), DisabledAt: timePointer(user.DisabledAt), DeletedAt: timePointer(user.DeletedAt), CreatedAt: user.CreatedAt.Time.UTC(), UpdatedAt: user.UpdatedAt.Time.UTC()}
}

func gatewayKeyFromDB(id, userID uuid.UUID, name, prefix string, expiresAt, revokedAt, lastUsedAt, createdAt pgtype.Timestamptz) identity.GatewayKey {
	return identity.GatewayKey{ID: id, UserID: userID, Name: name, Prefix: prefix, ExpiresAt: timePointer(expiresAt), RevokedAt: timePointer(revokedAt), LastUsedAt: timePointer(lastUsedAt), CreatedAt: createdAt.Time.UTC()}
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
	normalized := value.Time.UTC()
	return &normalized
}

func auditParams(actorID *uuid.UUID, action, targetType, targetID string, detail map[string]any) db.CreateAuditEventParams {
	if detail == nil {
		detail = map[string]any{}
	}
	encoded, _ := json.Marshal(detail)
	return db.CreateAuditEventParams{ActorUserID: actorID, Action: action, TargetType: targetType, TargetID: &targetID, Detail: encoded}
}

func waitForReconcile(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func translateStoreError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return identity.ErrNotFound
		case "23505", "40001":
			return identity.ErrConflict
		}
	}
	return fmt.Errorf("identity store: %w", err)
}
