package config

import (
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
