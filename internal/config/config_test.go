package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDevelopmentDefaults(t *testing.T) {
	t.Setenv("LLMGATEWAY_PROFILE", "development")
	t.Setenv("LLMGATEWAY_MASTER_KEYS", "")
	t.Setenv("LLMGATEWAY_SESSION_PEPPER", "")
	t.Setenv("LLMGATEWAY_API_KEY_PEPPER", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected explicitly empty secrets to fail validation")
	}
}

func TestProductionRequiresSecrets(t *testing.T) {
	t.Setenv("LLMGATEWAY_PROFILE", "production")
	t.Setenv("LLMGATEWAY_COOKIE_SECURE", "true")
	t.Setenv("LLMGATEWAY_MASTER_KEYS", "")
	t.Setenv("LLMGATEWAY_SESSION_PEPPER", "")
	t.Setenv("LLMGATEWAY_API_KEY_PEPPER", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected production configuration without secrets to fail")
	}
}

func TestLoadReadsProductionSecretsFromFiles(t *testing.T) {
	t.Setenv("LLMGATEWAY_PROFILE", "production")
	t.Setenv("LLMGATEWAY_COOKIE_SECURE", "true")
	t.Setenv("LLMGATEWAY_DATABASE_URL_FILE", writeSecret(t, "database-url", "postgres://llmgateway:password@postgres:5432/llmgateway?sslmode=require"))
	t.Setenv("LLMGATEWAY_VALKEY_PASSWORD_FILE", writeSecret(t, "valkey-password", strings.Repeat("v", 32)))
	masterKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	t.Setenv("LLMGATEWAY_MASTER_KEYS_FILE", writeSecret(t, "master-keys", "1:"+masterKey))
	t.Setenv("LLMGATEWAY_SESSION_PEPPER_FILE", writeSecret(t, "session-pepper", strings.Repeat("s", 32)))
	t.Setenv("LLMGATEWAY_API_KEY_PEPPER_FILE", writeSecret(t, "api-key-pepper", strings.Repeat("a", 32)))
	t.Setenv("LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE", writeSecret(t, "coordination-secret", strings.Repeat("c", 32)))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.URL != "postgres://llmgateway:password@postgres:5432/llmgateway?sslmode=require" || cfg.Valkey.Password != strings.Repeat("v", 32) {
		t.Fatal("file-backed secrets were not loaded")
	}
}

func TestLoadRejectsAmbiguousSecretSource(t *testing.T) {
	t.Setenv("LLMGATEWAY_DATABASE_URL", "postgres://from-environment")
	t.Setenv("LLMGATEWAY_DATABASE_URL_FILE", writeSecret(t, "database-url", "postgres://from-file"))

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "cannot both be set") {
		t.Fatalf("Load() error = %v, want ambiguous secret source", err)
	}
}

func TestLoadRejectsEmptySecretFile(t *testing.T) {
	t.Setenv("LLMGATEWAY_DATABASE_URL_FILE", writeSecret(t, "database-url", "\n"))

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "non-empty text secret") {
		t.Fatalf("Load() error = %v, want empty secret file rejection", err)
	}
}

func writeSecret(t *testing.T, name, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return path
}
