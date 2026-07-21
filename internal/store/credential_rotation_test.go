package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/security"
	"github.com/luckymaomi/llmgateway/migrations"
)

func TestProviderCredentialMasterKeyRotationIsAtomicAndIdempotent(t *testing.T) {
	databaseURL := os.Getenv("LLMGATEWAY_OPERATIONS_TEST_DATABASE_URL")
	if databaseURL == "" {
		if os.Getenv("LLMGATEWAY_OPERATIONS_TEST_REQUIRED") == "true" {
			t.Fatal("LLMGATEWAY_OPERATIONS_TEST_DATABASE_URL is required")
		}
		t.Skip("isolated PostgreSQL is required for credential rotation")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if err := migrations.Up(ctx, database); err != nil {
		t.Fatal(err)
	}

	providerID, credentialID := uuid.New(), uuid.New()
	oldKey := []byte("0123456789abcdef0123456789abcdef")
	newKey := []byte("fedcba9876543210fedcba9876543210")
	oldEnvelope, err := security.NewEnvelopeCipher(1, map[uint32][]byte{1: oldKey})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := oldEnvelope.Encrypt([]byte("rotation-secret"), registry.CredentialEncryptionContext(credentialID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO providers (id, slug, name, kind, base_url, enabled) VALUES ($1, $2, 'Rotation Provider', 'openai-compatible', 'https://provider.example.test/v1', true)`, providerID, "rotation-"+providerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO provider_credentials (id, provider_id, name, encrypted_secret, resource_domain, status) VALUES ($1, $2, 'Rotation Credential', $3, 'free', 'active')`, credentialID, providerID, encrypted); err != nil {
		t.Fatal(err)
	}

	rotatingEnvelope, err := security.NewEnvelopeCipher(2, map[uint32][]byte{1: oldKey, 2: newKey})
	if err != nil {
		t.Fatal(err)
	}
	result, err := RotateProviderCredentialEncryption(ctx, database, rotatingEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 1 || result.Rotated != 1 || result.ActiveKeyVersion != 2 {
		t.Fatalf("rotation result = %#v", result)
	}
	var rotated []byte
	if err := database.QueryRowContext(ctx, `SELECT encrypted_secret FROM provider_credentials WHERE id = $1`, credentialID).Scan(&rotated); err != nil {
		t.Fatal(err)
	}
	metadata, err := rotatingEnvelope.Metadata(rotated)
	if err != nil || metadata.KeyVersion != 2 {
		t.Fatalf("rotated metadata = %#v, error = %v", metadata, err)
	}
	plaintext, err := rotatingEnvelope.Decrypt(rotated, registry.CredentialEncryptionContext(credentialID))
	if err != nil || string(plaintext) != "rotation-secret" {
		t.Fatalf("rotated credential did not decrypt: %q, %v", plaintext, err)
	}
	var auditCount int
	if err := database.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE action = 'credential.encryption_rotated' AND target_id = $1`, credentialID.String()).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("rotation audit count = %d, error = %v", auditCount, err)
	}
	replayed, err := RotateProviderCredentialEncryption(ctx, database, rotatingEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Scanned != 1 || replayed.Rotated != 0 || replayed.ActiveKeyVersion != 2 {
		t.Fatalf("idempotent rotation result = %#v", replayed)
	}
}
