package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestIdentityRepositoryResolvesCurrentDisplayNamesInOneBatch(t *testing.T) {
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
