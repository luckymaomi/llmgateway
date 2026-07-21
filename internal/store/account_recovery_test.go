package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/security"
)

func TestAccountRecoveryPersistsPasswordSessionAndAuditFacts(t *testing.T) {
	pool := gatewayKeyRevocationTestPool(t)
	ctx := context.Background()
	administratorID := insertRecoveryUser(t, pool, identity.RoleAdministrator, identity.StatusActive)
	memberID := insertRecoveryUser(t, pool, identity.RoleMember, identity.StatusActive)
	insertRecoverySessions(t, pool, memberID, 2)
	repository := NewIdentityRepository(pool)
	replacementHash, err := identity.HashRecoveryPassword("member replacement password")
	if err != nil {
		t.Fatal(err)
	}
	mutation := identity.PasswordResetMutation{
		IdempotencyKey: uuid.New(), RequestFingerprint: bytes.Repeat([]byte{0x42}, 32), RequestID: "member-password-reset-test",
	}

	first, err := repository.ResetMemberPassword(ctx, memberID, replacementHash, administratorID, mutation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.ResetMemberPassword(ctx, memberID, "must-not-be-persisted", administratorID, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if first.RevokedSessions != 2 || second != first {
		t.Fatalf("password reset replay results = %+v / %+v", first, second)
	}

	var persistedHash string
	var activeSessions, mutationCount, auditCount int
	if err := pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", memberID).Scan(&persistedHash); err != nil {
		t.Fatal(err)
	}
	verification, err := security.VerifyPassword("member replacement password", persistedHash, security.RecommendedPasswordParameters())
	if err != nil || !verification.Match {
		t.Fatalf("persisted member password did not verify: %+v / %v", verification, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL", memberID).Scan(&activeSessions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM member_password_reset_mutations WHERE actor_user_id = $1 AND idempotency_key = $2", administratorID, mutation.IdempotencyKey).Scan(&mutationCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'identity.member_password_reset' AND target_id = $1", memberID.String()).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if activeSessions != 0 || mutationCount != 1 || auditCount != 1 {
		t.Fatalf("member recovery active sessions/mutations/audits = %d/%d/%d", activeSessions, mutationCount, auditCount)
	}

	conflict := mutation
	conflict.RequestFingerprint = bytes.Repeat([]byte{0x24}, 32)
	if _, err := repository.ResetMemberPassword(ctx, memberID, replacementHash, administratorID, conflict); !errors.Is(err, identity.ErrIdempotencyConflict) {
		t.Fatalf("password reset conflicting replay error = %v", err)
	}
}

func TestOfflineAdministratorRecoveryRejectsMembersAndRevokesSessions(t *testing.T) {
	pool := gatewayKeyRevocationTestPool(t)
	ctx := context.Background()
	administratorID := insertRecoveryUser(t, pool, identity.RoleAdministrator, identity.StatusDisabled)
	memberID := insertRecoveryUser(t, pool, identity.RoleMember, identity.StatusActive)
	insertRecoverySessions(t, pool, administratorID, 2)

	database, err := sql.Open("pgx", os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	hash, err := identity.HashRecoveryPassword("administrator replacement password")
	if err != nil {
		t.Fatal(err)
	}
	memberEmail := recoveryEmail(memberID)
	if _, err := RecoverAdministratorAccess(ctx, database, memberEmail, hash); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("member offline recovery error = %v, want ErrForbidden", err)
	}
	result, err := RecoverAdministratorAccess(ctx, database, recoveryEmail(administratorID), hash)
	if err != nil {
		t.Fatal(err)
	}
	if result.RevokedSessions != 2 {
		t.Fatalf("offline recovery revoked sessions = %d", result.RevokedSessions)
	}

	var status, persistedHash string
	var activeSessions, auditCount int
	var auditActorIsNull bool
	if err := pool.QueryRow(ctx, "SELECT status::text, password_hash FROM users WHERE id = $1", administratorID).Scan(&status, &persistedHash); err != nil {
		t.Fatal(err)
	}
	verification, err := security.VerifyPassword("administrator replacement password", persistedHash, security.RecommendedPasswordParameters())
	if err != nil || !verification.Match || status != string(identity.StatusActive) {
		t.Fatalf("offline recovery status/hash = %s/%+v/%v", status, verification, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL", administratorID).Scan(&activeSessions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*), bool_and(actor_user_id IS NULL) FROM audit_events WHERE action = 'identity.administrator_recovered' AND target_id = $1", administratorID.String()).Scan(&auditCount, &auditActorIsNull); err != nil {
		t.Fatal(err)
	}
	if activeSessions != 0 || auditCount != 1 || !auditActorIsNull {
		t.Fatalf("offline recovery active sessions/audits/system actor = %d/%d/%t", activeSessions, auditCount, auditActorIsNull)
	}
}

func TestSessionRevocationPreservesAdministratorCurrentSession(t *testing.T) {
	pool := gatewayKeyRevocationTestPool(t)
	ctx := context.Background()
	administratorID := insertRecoveryUser(t, pool, identity.RoleAdministrator, identity.StatusActive)
	sessionIDs := insertRecoverySessions(t, pool, administratorID, 3)
	repository := NewIdentityRepository(pool)

	result, err := repository.RevokeUserSessions(ctx, administratorID, administratorID, &sessionIDs[0], "administrator-session-revoke")
	if err != nil {
		t.Fatal(err)
	}
	var activeSessions int
	var activeSessionID uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT count(*), min(id::text)::uuid FROM sessions WHERE user_id = $1 AND revoked_at IS NULL", administratorID).Scan(&activeSessions, &activeSessionID); err != nil {
		t.Fatal(err)
	}
	if result.RevokedSessions != 2 || activeSessions != 1 || activeSessionID != sessionIDs[0] {
		t.Fatalf("session revoke result/active/current = %d/%d/%s, want 2/1/%s", result.RevokedSessions, activeSessions, activeSessionID, sessionIDs[0])
	}
}

func insertRecoveryUser(t *testing.T, pool *pgxpool.Pool, role identity.Role, status identity.Status) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `INSERT INTO users (
id, email, display_name, password_hash, role, status, approved_at, disabled_at
) VALUES ($1, $2, 'Recovery fixture', 'fixture-password-hash', $3::user_role, $4::user_status, now(), CASE WHEN $4::user_status = 'disabled' THEN now() ELSE NULL END)`, id, recoveryEmail(id), role, status)
	if err != nil {
		t.Fatalf("insert recovery user: %v", err)
	}
	return id
}

func insertRecoverySessions(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, count int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, 0, count)
	for range count {
		id := uuid.New()
		token := uuid.NewString()
		if _, err := pool.Exec(context.Background(), `INSERT INTO sessions (id, user_id, token_digest, csrf_digest, expires_at)
VALUES ($1, $2, $3, $4, $5)`, id, userID, []byte(token), []byte("csrf-"+token), time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatalf("insert recovery session: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func recoveryEmail(userID uuid.UUID) string {
	return "recovery-" + userID.String() + "@example.test"
}
