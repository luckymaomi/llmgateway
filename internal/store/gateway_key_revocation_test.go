package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestGatewayKeyRevocationIsIdempotentAndAuditedOnce(t *testing.T) {
	pool := gatewayKeyRevocationTestPool(t)
	ctx := context.Background()
	ownerID := insertGatewayKeyRevocationUser(t, pool, identity.RoleMember)
	administratorID := insertGatewayKeyRevocationUser(t, pool, identity.RoleAdministrator)
	ownerRevokedKeyID := insertGatewayKeyRevocationKey(t, pool, ownerID)
	administratorRevokedKeyID := insertGatewayKeyRevocationKey(t, pool, ownerID)
	repository := NewIdentityRepository(pool)
	commitResponseLost := errors.New("fixture: commit response lost")
	commitCalls := 0
	repository.commitGatewayKeyRevocation = func(ctx context.Context, tx pgx.Tx) error {
		commitCalls++
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return commitResponseLost
	}

	if err := repository.RevokeGatewayKey(ctx, ownerRevokedKeyID, ownerID, false); err != nil {
		t.Fatalf("RevokeGatewayKey(owner after committed response loss) error = %v", err)
	}
	if err := repository.RevokeGatewayKey(ctx, ownerRevokedKeyID, ownerID, false); err != nil {
		t.Fatalf("RevokeGatewayKey(owner replay) error = %v", err)
	}
	if err := repository.RevokeGatewayKey(ctx, ownerRevokedKeyID, administratorID, true); err != nil {
		t.Fatalf("RevokeGatewayKey(administrator replay) error = %v", err)
	}
	if err := repository.RevokeGatewayKey(ctx, administratorRevokedKeyID, administratorID, true); err != nil {
		t.Fatalf("RevokeGatewayKey(administrator after committed response loss) error = %v", err)
	}
	if err := repository.RevokeGatewayKey(ctx, administratorRevokedKeyID, administratorID, true); err != nil {
		t.Fatalf("RevokeGatewayKey(administrator replay) error = %v", err)
	}
	if commitCalls != 2 {
		t.Fatalf("gateway key revocation commit calls = %d, want 2", commitCalls)
	}

	for _, expectation := range []struct {
		keyID   uuid.UUID
		actorID uuid.UUID
	}{
		{keyID: ownerRevokedKeyID, actorID: ownerID},
		{keyID: administratorRevokedKeyID, actorID: administratorID},
	} {
		var revoked bool
		var auditCount int
		var auditActorID uuid.UUID
		if err := pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM gateway_keys WHERE id = $1", expectation.keyID).Scan(&revoked); err != nil {
			t.Fatalf("read revoked gateway key %s: %v", expectation.keyID, err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*), min(actor_user_id::text)::uuid FROM audit_events
WHERE action = 'gateway_key.revoked' AND target_type = 'gateway_key' AND target_id = $1`, expectation.keyID.String()).Scan(&auditCount, &auditActorID); err != nil {
			t.Fatalf("read gateway key revocation audit %s: %v", expectation.keyID, err)
		}
		if !revoked || auditCount != 1 || auditActorID != expectation.actorID {
			t.Fatalf("revoked/audit count/actor for %s = %t/%d/%s, want true/1/%s", expectation.keyID, revoked, auditCount, auditActorID, expectation.actorID)
		}
	}
}

func TestGatewayKeyRevocationDistinguishesMissingAndForbidden(t *testing.T) {
	pool := gatewayKeyRevocationTestPool(t)
	ctx := context.Background()
	ownerID := insertGatewayKeyRevocationUser(t, pool, identity.RoleMember)
	otherMemberID := insertGatewayKeyRevocationUser(t, pool, identity.RoleMember)
	keyID := insertGatewayKeyRevocationKey(t, pool, ownerID)
	repository := NewIdentityRepository(pool)

	if err := repository.RevokeGatewayKey(ctx, uuid.New(), ownerID, false); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("RevokeGatewayKey(missing) error = %v, want ErrNotFound", err)
	}
	if err := repository.RevokeGatewayKey(ctx, keyID, otherMemberID, false); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("RevokeGatewayKey(foreign owner) error = %v, want ErrForbidden", err)
	}

	var revoked bool
	var auditCount int
	if err := pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM gateway_keys WHERE id = $1", keyID).Scan(&revoked); err != nil {
		t.Fatalf("read protected gateway key: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events
WHERE action = 'gateway_key.revoked' AND target_type = 'gateway_key' AND target_id = $1`, keyID.String()).Scan(&auditCount); err != nil {
		t.Fatalf("count forbidden gateway key revocation audits: %v", err)
	}
	if revoked || auditCount != 0 {
		t.Fatalf("revoked/audit count after forbidden command = %t/%d, want false/0", revoked, auditCount)
	}
}

func TestGatewayKeyRevocationConcurrentReplayCommitsOneAudit(t *testing.T) {
	pool := gatewayKeyRevocationTestPool(t)
	ctx := context.Background()
	ownerID := insertGatewayKeyRevocationUser(t, pool, identity.RoleMember)
	keyID := insertGatewayKeyRevocationKey(t, pool, ownerID)
	repository := NewIdentityRepository(pool)

	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			errorsByCall <- repository.RevokeGatewayKey(ctx, keyID, ownerID, false)
		}()
	}
	ready.Wait()
	close(start)
	for call := 1; call <= 2; call++ {
		if err := <-errorsByCall; err != nil {
			t.Fatalf("concurrent RevokeGatewayKey() call %d error = %v", call, err)
		}
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events
WHERE action = 'gateway_key.revoked' AND target_type = 'gateway_key' AND target_id = $1`, keyID.String()).Scan(&auditCount); err != nil {
		t.Fatalf("count concurrent gateway key revocation audits: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("concurrent gateway key revocation audits = %d, want 1", auditCount)
	}
}

func gatewayKeyRevocationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_CONTROL_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the gateway key revocation repository test")
		}
		t.Skip("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the isolated gateway key revocation repository test")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := migrations.Up(ctx, database); err != nil {
		t.Fatalf("migrations.Up() error = %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func insertGatewayKeyRevocationUser(t *testing.T, pool *pgxpool.Pool, role identity.Role) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := "gateway-key-revocation-" + id.String() + "@example.test"
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (id, email, display_name, password_hash, role, status)
VALUES ($1, $2, 'Gateway Key Revocation Fixture', 'fixture-hash', $3, 'active')`, id, email, role); err != nil {
		t.Fatalf("insert gateway key revocation user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM audit_events WHERE actor_user_id = $1", id); err != nil {
			t.Errorf("delete gateway key revocation audits: %v", err)
		}
		if _, err := pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", id); err != nil {
			t.Errorf("delete gateway key revocation user: %v", err)
		}
	})
	return id
}

func insertGatewayKeyRevocationKey(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	digest := []byte("gateway-key-revocation-" + id.String())
	if _, err := pool.Exec(context.Background(), `INSERT INTO gateway_keys (id, user_id, name, prefix, secret_digest)
VALUES ($1, $2, 'Revocation Fixture', $3, $4)`, id, ownerID, "llmg_"+id.String()[:12], digest); err != nil {
		t.Fatalf("insert gateway key revocation fixture: %v", err)
	}
	return id
}
