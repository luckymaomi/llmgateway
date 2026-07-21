package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/security"
)

type CredentialRotationResult struct {
	Scanned          int
	Rotated          int
	ActiveKeyVersion uint32
}

type encryptedCredential struct {
	id        uuid.UUID
	encrypted []byte
}

func RotateProviderCredentialEncryption(ctx context.Context, database *sql.DB, envelope *security.EnvelopeCipher) (CredentialRotationResult, error) {
	if database == nil || envelope == nil {
		return CredentialRotationResult{}, fmt.Errorf("credential rotation dependencies are required")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return CredentialRotationResult{}, fmt.Errorf("begin credential rotation: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id, encrypted_secret FROM provider_credentials ORDER BY id FOR UPDATE`)
	if err != nil {
		return CredentialRotationResult{}, fmt.Errorf("lock credentials for rotation: %w", err)
	}
	var credentials []encryptedCredential
	for rows.Next() {
		var credential encryptedCredential
		if err := rows.Scan(&credential.id, &credential.encrypted); err != nil {
			rows.Close()
			return CredentialRotationResult{}, fmt.Errorf("read credential for rotation: %w", err)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Close(); err != nil {
		return CredentialRotationResult{}, fmt.Errorf("close credential rotation scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return CredentialRotationResult{}, fmt.Errorf("scan credentials for rotation: %w", err)
	}

	result := CredentialRotationResult{Scanned: len(credentials)}
	requestID := "credential-key-rotation-" + uuid.NewString()
	for _, credential := range credentials {
		contextData := registry.CredentialEncryptionContext(credential.id)
		needsRotation, err := envelope.NeedsReencryption(credential.encrypted, contextData)
		if err != nil {
			return CredentialRotationResult{}, fmt.Errorf("authenticate credential %s before rotation: %w", credential.id, err)
		}
		metadata, err := envelope.Metadata(credential.encrypted)
		if err != nil {
			return CredentialRotationResult{}, fmt.Errorf("read credential %s key version: %w", credential.id, err)
		}
		if result.ActiveKeyVersion == 0 && !needsRotation {
			result.ActiveKeyVersion = metadata.KeyVersion
		}
		if !needsRotation {
			continue
		}
		plaintext, err := envelope.Decrypt(credential.encrypted, contextData)
		if err != nil {
			return CredentialRotationResult{}, fmt.Errorf("decrypt credential %s: %w", credential.id, err)
		}
		rotated, err := envelope.Encrypt(plaintext, contextData)
		if err != nil {
			return CredentialRotationResult{}, fmt.Errorf("reencrypt credential %s: %w", credential.id, err)
		}
		rotatedMetadata, err := envelope.Metadata(rotated)
		if err != nil {
			return CredentialRotationResult{}, fmt.Errorf("read rotated credential %s key version: %w", credential.id, err)
		}
		result.ActiveKeyVersion = rotatedMetadata.KeyVersion
		if _, err := tx.ExecContext(ctx, `UPDATE provider_credentials SET encrypted_secret = $2, updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond') WHERE id = $1`, credential.id, rotated); err != nil {
			return CredentialRotationResult{}, fmt.Errorf("persist rotated credential %s: %w", credential.id, err)
		}
		detail, err := json.Marshal(map[string]uint32{"from_key_version": metadata.KeyVersion, "to_key_version": rotatedMetadata.KeyVersion})
		if err != nil {
			return CredentialRotationResult{}, fmt.Errorf("encode credential rotation audit: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events (actor_user_id, action, target_type, target_id, request_id, detail) VALUES (NULL, 'credential.encryption_rotated', 'credential', $1, $2, $3::jsonb)`, credential.id.String(), requestID, detail); err != nil {
			return CredentialRotationResult{}, fmt.Errorf("audit rotated credential %s: %w", credential.id, err)
		}
		result.Rotated++
	}
	if err := tx.Commit(); err != nil {
		return CredentialRotationResult{}, fmt.Errorf("commit credential rotation: %w", err)
	}
	return result, nil
}
