package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type AdministratorRecoveryResult struct {
	RevokedSessions int64
}

func RecoverAdministratorAccess(ctx context.Context, database *sql.DB, email, passwordHash string) (AdministratorRecoveryResult, error) {
	if database == nil || email == "" || passwordHash == "" {
		return AdministratorRecoveryResult{}, identity.ErrInvalidInput
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return AdministratorRecoveryResult{}, err
	}
	defer tx.Rollback()
	var userID uuid.UUID
	var role, previousStatus string
	if err := tx.QueryRowContext(ctx, `SELECT id, role::text, status::text FROM users WHERE lower(email) = lower($1) FOR UPDATE`, email).Scan(&userID, &role, &previousStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdministratorRecoveryResult{}, identity.ErrNotFound
		}
		return AdministratorRecoveryResult{}, err
	}
	if role != string(identity.RoleAdministrator) {
		return AdministratorRecoveryResult{}, identity.ErrForbidden
	}
	result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = $1, status = 'active', disabled_at = NULL, approved_at = coalesce(approved_at, now()), updated_at = now() WHERE id = $2`, passwordHash, userID)
	if err != nil {
		return AdministratorRecoveryResult{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return AdministratorRecoveryResult{}, err
	}
	if rows != 1 {
		return AdministratorRecoveryResult{}, fmt.Errorf("administrator recovery updated %d users", rows)
	}
	revokedResult, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return AdministratorRecoveryResult{}, err
	}
	revoked, err := revokedResult.RowsAffected()
	if err != nil {
		return AdministratorRecoveryResult{}, err
	}
	detail, err := json.Marshal(map[string]any{"previous_status": previousStatus, "revoked_sessions": revoked})
	if err != nil {
		return AdministratorRecoveryResult{}, err
	}
	requestID := "dbtool-administrator-recovery-" + uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events (actor_user_id, action, target_type, target_id, request_id, detail) VALUES (NULL, 'identity.administrator_recovered', 'user', $1, $2, $3::jsonb)`, userID.String(), requestID, detail); err != nil {
		return AdministratorRecoveryResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdministratorRecoveryResult{}, err
	}
	return AdministratorRecoveryResult{RevokedSessions: revoked}, nil
}
