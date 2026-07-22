package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestIdentityRepositoryCommitsBootstrapAndPasswordChangeAsIdentityFacts(t *testing.T) {
	pool := identityDisplayNameTestPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE system_state SET bootstrapped_at = NULL WHERE singleton = true"); err != nil {
		t.Fatalf("reset bootstrap fixture: %v", err)
	}
	var existingUsers int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&existingUsers); err != nil {
		t.Fatalf("count identity fixtures: %v", err)
	}
	if existingUsers != 0 {
		t.Fatalf("identity repository test requires an isolated empty database, found %d users", existingUsers)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM audit_events WHERE action IN ('system.bootstrap', 'identity.password_changed')"); err != nil {
			t.Errorf("delete identity transaction audits: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE email LIKE 'bootstrap-transaction-%@example.test'"); err != nil {
			t.Errorf("delete identity transaction users: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "UPDATE system_state SET bootstrapped_at = NULL WHERE singleton = true"); err != nil {
			t.Errorf("reset bootstrap state: %v", err)
		}
	})

	fixtureUserID := uuid.New()
	duplicateDigest := make([]byte, 32)
	duplicateDigest[0] = 1
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at)
VALUES ($1, 'bootstrap-transaction-fixture@example.test', 'Fixture', 'fixture-hash', 'member', 'active', now())`, fixtureUserID); err != nil {
		t.Fatalf("insert rollback fixture user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (user_id, token_digest, csrf_digest, expires_at)
VALUES ($1, $2, $3, now() + interval '1 hour')`, fixtureUserID, duplicateDigest, make([]byte, 32)); err != nil {
		t.Fatalf("insert rollback fixture session: %v", err)
	}

	repository := NewIdentityRepository(pool)
	failedEmail := "bootstrap-transaction-failed@example.test"
	_, err := repository.Bootstrap(ctx, identity.NewUser{
		Email: failedEmail, DisplayName: "Administrator", PasswordHash: "failed-hash",
		Role: identity.RoleAdministrator, Status: identity.StatusActive,
	}, identity.SessionCreation{TokenDigest: duplicateDigest, CSRFDigest: make([]byte, 32), ExpiresAt: time.Now().Add(time.Hour)})
	if !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("Bootstrap(duplicate session) error = %v, want ErrConflict", err)
	}
	var failedUsers, failedAudits int
	var bootstrapped bool
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email = $1", failedEmail).Scan(&failedUsers); err != nil {
		t.Fatalf("count rolled-back bootstrap user: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'system.bootstrap'").Scan(&failedAudits); err != nil {
		t.Fatalf("count rolled-back bootstrap audit: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT bootstrapped_at IS NOT NULL FROM system_state WHERE singleton = true").Scan(&bootstrapped); err != nil {
		t.Fatalf("read rolled-back bootstrap state: %v", err)
	}
	if failedUsers != 0 || failedAudits != 0 || bootstrapped {
		t.Fatalf("failed bootstrap left user/audit/state = %d/%d/%t", failedUsers, failedAudits, bootstrapped)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", fixtureUserID); err != nil {
		t.Fatalf("delete rollback fixture: %v", err)
	}

	type bootstrapResult struct {
		principal identity.Principal
		err       error
	}
	start := make(chan struct{})
	results := make(chan bootstrapResult, 2)
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			tokenDigest := make([]byte, 32)
			tokenDigest[0] = byte(index + 10)
			csrfDigest := make([]byte, 32)
			csrfDigest[0] = byte(index + 20)
			principal, bootstrapErr := repository.Bootstrap(ctx, identity.NewUser{
				Email: "bootstrap-transaction-" + string(rune('a'+index)) + "@example.test", DisplayName: "Administrator",
				PasswordHash: "initial-hash", Role: identity.RoleAdministrator, Status: identity.StatusActive,
			}, identity.SessionCreation{TokenDigest: tokenDigest, CSRFDigest: csrfDigest, ExpiresAt: time.Now().Add(time.Hour)})
			results <- bootstrapResult{principal: principal, err: bootstrapErr}
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	var winner identity.Principal
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.principal
		case errors.Is(result.err, identity.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent Bootstrap() error = %v", result.err)
		}
	}
	var administrators, activeSessions, bootstrapAudits int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE email LIKE 'bootstrap-transaction-%@example.test'").Scan(&administrators); err != nil {
		t.Fatalf("count bootstrap administrators: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL", winner.UserID).Scan(&activeSessions); err != nil {
		t.Fatalf("count bootstrap sessions: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'system.bootstrap' AND actor_user_id = $1", winner.UserID).Scan(&bootstrapAudits); err != nil {
		t.Fatalf("count bootstrap audits: %v", err)
	}
	if successes != 1 || conflicts != 1 || administrators != 1 || activeSessions != 1 || bootstrapAudits != 1 {
		t.Fatalf("concurrent bootstrap success/conflict/users/sessions/audits = %d/%d/%d/%d/%d", successes, conflicts, administrators, activeSessions, bootstrapAudits)
	}

	for index := range 2 {
		digest := make([]byte, 32)
		digest[0] = byte(index + 40)
		if _, err := pool.Exec(ctx, `INSERT INTO sessions (user_id, token_digest, csrf_digest, expires_at)
VALUES ($1, $2, $3, now() + interval '1 hour')`, winner.UserID, digest, make([]byte, 32)); err != nil {
			t.Fatalf("insert additional session: %v", err)
		}
	}
	changed, err := repository.ChangeOwnPassword(ctx, winner.UserID, winner.SessionID, "initial-hash", "replacement-hash", "password-change-request")
	if err != nil {
		t.Fatalf("ChangeOwnPassword() error = %v", err)
	}
	var currentActive, otherActive, passwordAudits int
	var passwordHash string
	if err := pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", winner.UserID).Scan(&passwordHash); err != nil {
		t.Fatalf("read replacement password hash: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL", winner.SessionID).Scan(&currentActive); err != nil {
		t.Fatalf("count current session: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL", winner.UserID, winner.SessionID).Scan(&otherActive); err != nil {
		t.Fatalf("count other sessions: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'identity.password_changed' AND actor_user_id = $1 AND request_id = 'password-change-request'", winner.UserID).Scan(&passwordAudits); err != nil {
		t.Fatalf("count password change audits: %v", err)
	}
	if changed.RevokedSessions != 2 || passwordHash != "replacement-hash" || currentActive != 1 || otherActive != 0 || passwordAudits != 1 {
		t.Fatalf("password change revoked/hash/current/other/audits = %d/%q/%d/%d/%d", changed.RevokedSessions, passwordHash, currentActive, otherActive, passwordAudits)
	}
	if _, err := repository.ChangeOwnPassword(ctx, winner.UserID, winner.SessionID, "initial-hash", "stale-overwrite", "stale-password-change"); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("ChangeOwnPassword(stale hash) error = %v, want ErrConflict", err)
	}
	if err := pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", winner.UserID).Scan(&passwordHash); err != nil {
		t.Fatalf("read password after stale change: %v", err)
	}
	if passwordHash != "replacement-hash" {
		t.Fatalf("stale password change overwrote hash with %q", passwordHash)
	}
}

func TestIdentityRepositoryResolvesCurrentDisplayNames(t *testing.T) {
	pool := identityDisplayNameTestPool(t)
	ctx := context.Background()
	firstUserID, secondUserID := uuid.New(), uuid.New()
	for _, user := range []struct {
		id          uuid.UUID
		displayName string
	}{
		{id: firstUserID, displayName: "First Creator"},
		{id: secondUserID, displayName: "Second Creator"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at)
VALUES ($1, $2, $3, 'fixture-hash', 'administrator', 'active', now())`, user.id, "display-name-"+user.id.String()+"@example.test", user.displayName); err != nil {
			t.Fatalf("insert identity display-name fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM users WHERE id = ANY($1::uuid[])", []uuid.UUID{firstUserID, secondUserID}); err != nil {
			t.Errorf("delete identity display-name fixtures: %v", err)
		}
	})

	repository := NewIdentityRepository(pool)
	empty, err := repository.UserDisplayNames(ctx, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty UserDisplayNames() = %v, %v", empty, err)
	}
	names, err := repository.UserDisplayNames(ctx, []uuid.UUID{secondUserID, firstUserID, secondUserID})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[firstUserID] != "First Creator" || names[secondUserID] != "Second Creator" {
		t.Fatalf("UserDisplayNames() = %v", names)
	}

	if _, err := pool.Exec(ctx, "UPDATE users SET display_name = 'Renamed Creator' WHERE id = $1", firstUserID); err != nil {
		t.Fatalf("rename identity display-name fixture: %v", err)
	}
	refreshed, err := repository.UserDisplayNames(ctx, []uuid.UUID{firstUserID})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed[firstUserID] != "Renamed Creator" {
		t.Fatalf("refreshed display name = %q, want Renamed Creator", refreshed[firstUserID])
	}
}

func identityDisplayNameTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_CONTROL_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the identity display-name repository test")
		}
		t.Skip("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the isolated identity display-name repository test")
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
