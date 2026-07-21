package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestInvitationMutationReconcilesCommittedResultWithoutPersistingCode(t *testing.T) {
	pool := invitationMutationTestPool(t)
	ctx := context.Background()
	actorID := insertInvitationMutationActor(t, pool)
	completeCode := "invite_store_commit_reconciliation_secret"
	input := invitationMutationInput(completeCode)
	mutation := invitationMutation("invitation-commit-reconciliation")
	repository := NewIdentityRepository(pool)
	commitResponseLost := errors.New("fixture: commit response lost")
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	commitCalls := 0
	repository.commitInvitationMutation = func(ctx context.Context, tx pgx.Tx) error {
		commitCalls++
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		cancelRequest()
		return commitResponseLost
	}

	created, err := repository.CreateInvitation(requestCtx, input, actorID, mutation)
	if err != nil {
		t.Fatalf("CreateInvitation() after committed response loss error = %v", err)
	}
	requireInvitationResult(t, created, actorID, input)
	if commitCalls != 1 {
		t.Fatalf("invitation commit hook calls = %d, want 1", commitCalls)
	}
	assertInvitationMutationFacts(t, pool, actorID, mutation, input, created, completeCode)

	replayed, found, err := repository.ReplayInvitationMutation(ctx, actorID, mutation)
	if err != nil || !found {
		t.Fatalf("ReplayInvitationMutation() = %#v, %t, %v", replayed, found, err)
	}
	if replayed != created {
		t.Fatalf("replayed invitation = %#v, want %#v", replayed, created)
	}
	replayedThroughCreate, err := repository.CreateInvitation(ctx, input, actorID, mutation)
	if err != nil {
		t.Fatalf("CreateInvitation(replay) error = %v", err)
	}
	if replayedThroughCreate != created || commitCalls != 1 {
		t.Fatalf("CreateInvitation(replay) = %#v with %d commits, want %#v with 1", replayedThroughCreate, commitCalls, created)
	}

	conflicting := mutation
	conflictingFingerprint := sha256.Sum256([]byte("different invitation input"))
	conflicting.RequestFingerprint = conflictingFingerprint[:]
	conflicting.RequestID = "invitation-conflicting-replay"
	if _, found, err := repository.ReplayInvitationMutation(ctx, actorID, conflicting); !found || !errors.Is(err, identity.ErrIdempotencyConflict) {
		t.Fatalf("ReplayInvitationMutation(conflict) found/error = %t/%v", found, err)
	}
	if _, err := repository.CreateInvitation(ctx, input, actorID, conflicting); !errors.Is(err, identity.ErrIdempotencyConflict) {
		t.Fatalf("CreateInvitation(conflict) error = %v, want ErrIdempotencyConflict", err)
	}
	if commitCalls != 1 {
		t.Fatalf("conflicting replay invoked commit hook; calls = %d", commitCalls)
	}
	assertInvitationMutationFacts(t, pool, actorID, mutation, input, created, completeCode)
}

func TestInvitationMutationConcurrentReplayCreatesOneFact(t *testing.T) {
	pool := invitationMutationTestPool(t)
	ctx := context.Background()
	actorID := insertInvitationMutationActor(t, pool)
	completeCode := "invite_store_concurrent_replay_secret"
	input := invitationMutationInput(completeCode)
	mutation := invitationMutation("invitation-concurrent-replay")
	repository := NewIdentityRepository(pool)

	type outcome struct {
		invitation identity.Invitation
		err        error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			invitation, err := repository.CreateInvitation(ctx, input, actorID, mutation)
			outcomes <- outcome{invitation: invitation, err: err}
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-outcomes, <-outcomes
	for index, result := range []outcome{first, second} {
		if result.err != nil {
			t.Fatalf("concurrent CreateInvitation() result %d error = %v", index+1, result.err)
		}
		requireInvitationResult(t, result.invitation, actorID, input)
	}
	if first.invitation != second.invitation {
		t.Fatalf("concurrent invitations = %#v and %#v, want one fact", first.invitation, second.invitation)
	}
	assertInvitationMutationFacts(t, pool, actorID, mutation, input, first.invitation, completeCode)
}

func TestInvitationMutationAuditFailureRollsBackAllFactsAndCanRetry(t *testing.T) {
	pool := invitationMutationTestPool(t)
	ctx := context.Background()
	actorID := insertInvitationMutationActor(t, pool)
	completeCode := "invite_store_audit_rollback_secret"
	input := invitationMutationInput(completeCode)
	mutation := invitationMutation("invitation-audit-rollback")
	repository := NewIdentityRepository(pool)
	installInvitationAuditFailure(t, pool, actorID)

	if _, err := repository.CreateInvitation(ctx, input, actorID, mutation); err == nil {
		t.Fatal("CreateInvitation() succeeded despite forced audit failure")
	}
	assertInvitationMutationFactCounts(t, pool, actorID, 0, 0, 0)
	dropInvitationAuditFailure(t, pool)

	created, err := repository.CreateInvitation(ctx, input, actorID, mutation)
	if err != nil {
		t.Fatalf("CreateInvitation() retry after audit rollback error = %v", err)
	}
	assertInvitationMutationFacts(t, pool, actorID, mutation, input, created, completeCode)
}

func invitationMutationTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_CONTROL_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the invitation mutation repository test")
		}
		t.Skip("LLMGATEWAY_CONTROL_TEST_DATABASE_URL is required for the isolated invitation mutation repository test")
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

func insertInvitationMutationActor(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	actorID := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at)
VALUES ($1, $2, 'Invitation Mutation Admin', 'fixture-hash', 'administrator', 'active', now())`, actorID, "invitation-mutation-"+actorID.String()+"@example.test"); err != nil {
		t.Fatalf("insert invitation mutation actor: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, statement := range []string{
			"DELETE FROM audit_events WHERE actor_user_id = $1",
			"DELETE FROM invitation_mutations WHERE actor_user_id = $1",
			"DELETE FROM invitations WHERE created_by = $1",
			"DELETE FROM users WHERE id = $1",
		} {
			if _, err := pool.Exec(ctx, statement, actorID); err != nil {
				t.Errorf("cleanup invitation mutation fixture with %q: %v", statement, err)
			}
		}
	})
	return actorID
}

func invitationMutationInput(completeCode string) identity.NewInvitation {
	digest := sha256.Sum256([]byte(completeCode))
	return identity.NewInvitation{
		CodeDigest: digest[:], CodePrefix: completeCode[:13],
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond),
	}
}

func invitationMutation(requestID string) identity.InvitationMutation {
	fingerprint := sha256.Sum256([]byte(requestID))
	return identity.InvitationMutation{
		IdempotencyKey: uuid.New(), RequestFingerprint: fingerprint[:], RequestID: requestID,
	}
}

func requireInvitationResult(t *testing.T, actual identity.Invitation, actorID uuid.UUID, input identity.NewInvitation) {
	t.Helper()
	if actual.ID == uuid.Nil || actual.CreatedBy != actorID || actual.ClaimedBy != nil || !actual.ExpiresAt.Equal(input.ExpiresAt) || actual.ClaimedAt != nil || actual.RevokedAt != nil || actual.CreatedAt.IsZero() || actual.CodePrefix != input.CodePrefix || actual.Code != "" {
		t.Fatalf("invitation result = %#v", actual)
	}
}

func assertInvitationMutationFacts(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID, mutation identity.InvitationMutation, input identity.NewInvitation, invitation identity.Invitation, completeCode string) {
	t.Helper()
	ctx := context.Background()
	assertInvitationMutationFactCounts(t, pool, actorID, 1, 1, 1)

	var persistedDigest []byte
	var persistedPrefix string
	if err := pool.QueryRow(ctx, "SELECT code_digest, code_prefix FROM invitations WHERE id = $1", invitation.ID).Scan(&persistedDigest, &persistedPrefix); err != nil {
		t.Fatalf("read persisted invitation: %v", err)
	}
	if !bytes.Equal(persistedDigest, input.CodeDigest) || persistedPrefix != input.CodePrefix {
		t.Fatalf("persisted invitation digest/prefix = %x/%q", persistedDigest, persistedPrefix)
	}

	var mutationInvitationID uuid.UUID
	var persistedFingerprint []byte
	var mutationRequestID, mutationResult string
	if err := pool.QueryRow(ctx, `SELECT invitation_id, request_fingerprint, request_id, result::text
FROM invitation_mutations WHERE actor_user_id = $1 AND idempotency_key = $2`, actorID, mutation.IdempotencyKey).Scan(&mutationInvitationID, &persistedFingerprint, &mutationRequestID, &mutationResult); err != nil {
		t.Fatalf("read invitation mutation: %v", err)
	}
	if mutationInvitationID != invitation.ID || !bytes.Equal(persistedFingerprint, mutation.RequestFingerprint) || mutationRequestID != mutation.RequestID {
		t.Fatalf("persisted invitation mutation identity = %s/%x/%q", mutationInvitationID, persistedFingerprint, mutationRequestID)
	}
	var resultFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mutationResult), &resultFields); err != nil {
		t.Fatalf("decode invitation mutation result: %v", err)
	}
	if _, containsCode := resultFields["code"]; containsCode || strings.Contains(mutationResult, completeCode) {
		t.Fatalf("invitation mutation result persisted the complete code: %s", mutationResult)
	}
	if _, hasPrefix := resultFields["code_prefix"]; !hasPrefix {
		t.Fatalf("invitation mutation result omitted its non-sensitive prefix: %s", mutationResult)
	}

	var auditRequestID, auditDetail string
	if err := pool.QueryRow(ctx, `SELECT request_id, detail::text FROM audit_events
WHERE actor_user_id = $1 AND action = 'invitation.created' AND target_type = 'invitation' AND target_id = $2`, actorID, invitation.ID.String()).Scan(&auditRequestID, &auditDetail); err != nil {
		t.Fatalf("read invitation creation audit: %v", err)
	}
	if auditRequestID != mutation.RequestID || strings.Contains(auditDetail, completeCode) || strings.Contains(auditDetail, fmt.Sprintf("%x", input.CodeDigest)) {
		t.Fatalf("invitation audit request/detail = %q/%s", auditRequestID, auditDetail)
	}
	var detail struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(auditDetail), &detail); err != nil || !detail.ExpiresAt.Equal(input.ExpiresAt) {
		t.Fatalf("invitation audit detail = %#v, error = %v", detail, err)
	}
}

func assertInvitationMutationFactCounts(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID, invitations, mutations, audits int) {
	t.Helper()
	ctx := context.Background()
	queries := []struct {
		name string
		sql  string
		want int
	}{
		{name: "invitations", sql: "SELECT count(*) FROM invitations WHERE created_by = $1", want: invitations},
		{name: "mutations", sql: "SELECT count(*) FROM invitation_mutations WHERE actor_user_id = $1", want: mutations},
		{name: "audits", sql: "SELECT count(*) FROM audit_events WHERE actor_user_id = $1 AND action = 'invitation.created'", want: audits},
	}
	for _, query := range queries {
		var count int
		if err := pool.QueryRow(ctx, query.sql, actorID).Scan(&count); err != nil {
			t.Fatalf("count invitation mutation %s: %v", query.name, err)
		}
		if count != query.want {
			t.Fatalf("invitation mutation %s count = %d, want %d", query.name, count, query.want)
		}
	}
}

func installInvitationAuditFailure(t *testing.T, pool *pgxpool.Pool, actorID uuid.UUID) {
	t.Helper()
	statement := fmt.Sprintf(`CREATE OR REPLACE FUNCTION reject_invitation_create_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.action = 'invitation.created' AND NEW.actor_user_id = '%s'::uuid THEN
        RAISE EXCEPTION 'forced invitation audit failure';
    END IF;
    RETURN NEW;
END;
$$`, actorID)
	if _, err := pool.Exec(context.Background(), statement); err != nil {
		t.Fatalf("create invitation audit failure function: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS reject_invitation_create_audit ON audit_events"); err != nil {
		t.Fatalf("drop prior invitation audit failure trigger: %v", err)
	}
	if _, err := pool.Exec(context.Background(), "CREATE TRIGGER reject_invitation_create_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_invitation_create_audit()"); err != nil {
		t.Fatalf("create invitation audit failure trigger: %v", err)
	}
	t.Cleanup(func() { dropInvitationAuditFailure(t, pool) })
}

func dropInvitationAuditFailure(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "DROP TRIGGER IF EXISTS reject_invitation_create_audit ON audit_events"); err != nil {
		t.Errorf("drop invitation audit failure trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, "DROP FUNCTION IF EXISTS reject_invitation_create_audit()"); err != nil {
		t.Errorf("drop invitation audit failure function: %v", err)
	}
}
