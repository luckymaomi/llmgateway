package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/migrations"
	"github.com/redis/go-redis/v9"
)

func TestConfigurationPublicationPersistsImmutableSnapshots(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_CONTROL_TEST_DATABASE_URL")
	valkeyAddress := os.Getenv("LLMGATEWAY_CONTROL_TEST_VALKEY_ADDRESS")
	if databaseURL == "" || valkeyAddress == "" {
		if os.Getenv("LLMGATEWAY_CONTROL_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_CONTROL_TEST_DATABASE_URL and LLMGATEWAY_CONTROL_TEST_VALKEY_ADDRESS are required for the configuration repository test")
		}
		t.Skip("isolated PostgreSQL and Valkey are required for the configuration repository test")
	}

	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	if err := migrations.Up(ctx, database); err != nil {
		t.Fatalf("migrations.Up() error = %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()

	valkeyDatabase, err := strconv.Atoi(environmentValue("LLMGATEWAY_CONTROL_TEST_VALKEY_DATABASE", "0"))
	if err != nil {
		t.Fatalf("invalid LLMGATEWAY_CONTROL_TEST_VALKEY_DATABASE: %v", err)
	}
	valkey := redis.NewClient(&redis.Options{
		Addr:        valkeyAddress,
		Password:    os.Getenv("LLMGATEWAY_CONTROL_TEST_VALKEY_PASSWORD"),
		DB:          valkeyDatabase,
		DialTimeout: time.Second,
	})
	defer valkey.Close()
	if err := valkey.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping isolated Valkey: %v", err)
	}
	if err := valkey.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("clear isolated Valkey before configuration test: %v", err)
	}

	actorID := uuid.New()
	providerID := uuid.New()
	modelID := uuid.New()
	credentialID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status, approved_at)
VALUES ($1, $2, 'Configuration Test Admin', 'fixture-hash', 'administrator', 'active', now())`, actorID, "configuration-"+actorID.String()+"@example.test"); err != nil {
		t.Fatalf("insert configuration test actor: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO providers (id, slug, name, kind, base_url, enabled)
VALUES ($1, $2, 'Provider A', 'openai_compatible', 'https://198.18.0.30/v1', true)`, providerID, "configuration-"+providerID.String()); err != nil {
		t.Fatalf("insert configuration test Provider: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO models
  (id, provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, enabled)
VALUES ($1, $2, 'model-a', 'upstream-a', 'Model A', 'free', '{"chat":true}'::jsonb, true)`, modelID, providerID); err != nil {
		t.Fatalf("insert configuration test model: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO provider_credentials
  (id, provider_id, name, encrypted_secret, resource_domain, status, rpm_limit, tpm_limit, concurrency_limit)
VALUES ($1, $2, 'Credential A', $3, 'free', 'active', 10, 1000, 2)`, credentialID, providerID, []byte("fixture-encrypted-secret")); err != nil {
		t.Fatalf("insert configuration test credential: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO credential_models (credential_id, model_id, priority, weight)
VALUES ($1, $2, 10, 80)`, credentialID, modelID); err != nil {
		t.Fatalf("insert configuration test route: %v", err)
	}
	defer cleanupConfigurationTestFacts(t, pool, valkey, actorID, providerID)

	repository := NewConfigurationRepository(&Connections{Postgres: pool, Valkey: valkey})
	service, err := configuration.NewService(repository)
	if err != nil {
		t.Fatalf("configuration.NewService() error = %v", err)
	}
	actor := identity.Principal{UserID: actorID, Role: identity.RoleAdministrator, Status: identity.StatusActive}

	revisionA := captureConfigurationRevision(t, service, actor, "capture-a")
	assertCatalogSummary(t, revisionA.Catalog, 1, 1, 1, 1)

	setLiveConfigurationState(t, pool, liveConfigurationState{
		providerID: providerID, modelID: modelID, credentialID: credentialID,
		providerName: "Provider B", baseURL: "https://198.18.0.32/v1",
		publicName: "model-b", upstreamName: "upstream-b", displayName: "Model B", capabilities: `{"chat":true,"tools":true}`,
		rpmLimit: 20, tpmLimit: 2000, concurrencyLimit: 3, priority: 20, weight: 60,
	})
	revisionB := captureConfigurationRevision(t, service, actor, "capture-b")

	setLiveConfigurationState(t, pool, liveConfigurationState{
		providerID: providerID, modelID: modelID, credentialID: credentialID,
		providerName: "Provider C", baseURL: "https://198.18.0.33/v1",
		publicName: "model-c", upstreamName: "upstream-c", displayName: "Model C", capabilities: `{"chat":true}`,
		rpmLimit: 30, tpmLimit: 3000, concurrencyLimit: 4, priority: 30, weight: 40,
	})
	revisionC := captureConfigurationRevision(t, service, actor, "capture-c")

	if _, err := pool.Exec(ctx, "DELETE FROM providers WHERE id = $1", providerID); err != nil {
		t.Fatalf("delete live registry after capture: %v", err)
	}

	activeA := publishConfigurationRevision(t, service, actor, revisionA.ID, 0, configuration.MutationPublish, "publish-a")
	if activeA.Version != 1 || activeA.Revision.ID != revisionA.ID {
		t.Fatalf("first active configuration = revision %s version %d, want revision A version 1", activeA.Revision.ID, activeA.Version)
	}
	assertPublishedCatalogA(t, repository)

	beforeNoopAudits := countConfigurationFacts(t, pool, `SELECT count(*) FROM audit_events
WHERE actor_user_id = $1 AND action IN ('configuration.published', 'configuration.rolled_back')`, actorID)
	noop := publishConfigurationRevision(t, service, actor, revisionA.ID, 1, configuration.MutationPublish, "publish-a-noop")
	if noop.Version != 1 || noop.Revision.ID != revisionA.ID {
		t.Fatalf("same-target publish = revision %s version %d, want unchanged revision A version 1", noop.Revision.ID, noop.Version)
	}
	assertConfigurationFactCount(t, pool, `SELECT count(*) FROM audit_events
WHERE actor_user_id = $1 AND action IN ('configuration.published', 'configuration.rolled_back')`, beforeNoopAudits, actorID)

	type publishOutcome struct {
		revision configuration.Revision
		active   configuration.Active
		err      error
	}
	start := make(chan struct{})
	outcomes := make(chan publishOutcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index, revision := range []configuration.Revision{revisionB, revisionC} {
		index, revision := index, revision
		go func() {
			ready.Done()
			<-start
			active, publishErr := service.Publish(ctx, actor, revision.ID, 1, configuration.MutationPublish, mutationRequest(fmt.Sprintf("publish-concurrent-%d", index)))
			outcomes <- publishOutcome{revision: revision, active: active, err: publishErr}
		}()
	}
	ready.Wait()
	close(start)
	first := <-outcomes
	second := <-outcomes

	var winner, loser publishOutcome
	switch {
	case first.err == nil && errors.Is(second.err, configuration.ErrConflict):
		winner, loser = first, second
	case second.err == nil && errors.Is(first.err, configuration.ErrConflict):
		winner, loser = second, first
	default:
		t.Fatalf("concurrent publish results = (%v, %v), want one success and one configuration conflict", first.err, second.err)
	}
	if winner.active.Version != 2 || winner.active.Revision.ID != winner.revision.ID {
		t.Fatalf("concurrent winner = revision %s version %d, want winning revision version 2", winner.active.Revision.ID, winner.active.Version)
	}
	current, err := repository.Active(ctx)
	if err != nil {
		t.Fatalf("Active() after concurrent publish error = %v", err)
	}
	if current.Version != 2 || current.Revision.ID != winner.revision.ID {
		t.Fatalf("persisted concurrent winner = revision %s version %d", current.Revision.ID, current.Version)
	}

	rolledBack := publishConfigurationRevision(t, service, actor, revisionA.ID, 2, configuration.MutationRollback, "rollback-a")
	if rolledBack.Version != 3 || rolledBack.Revision.ID != revisionA.ID {
		t.Fatalf("rolled back configuration = revision %s version %d, want revision A version 3", rolledBack.Revision.ID, rolledBack.Version)
	}
	assertConfigurationFactCount(t, pool, `SELECT count(*) FROM audit_events
WHERE actor_user_id = $1 AND action = 'configuration.rolled_back' AND target_id = $2`, 1, actorID, revisionA.ID.String())
	if _, err := service.Publish(ctx, actor, loser.revision.ID, 1, configuration.MutationPublish, mutationRequest("publish-stale-after-aba")); !errors.Is(err, configuration.ErrConflict) {
		t.Fatalf("publish with version observed before A-B-A error = %v, want ErrConflict", err)
	}
	assertPublishedCatalogA(t, repository)

}
