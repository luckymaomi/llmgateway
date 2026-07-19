package security

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestGenerateTokenProvidesURLSafeEntropy(t *testing.T) {
	first, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	second, err := GenerateToken()
	if err != nil {
		t.Fatalf("second GenerateToken() error = %v", err)
	}
	if first == second {
		t.Fatal("independent tokens must differ")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(first)
	if err != nil {
		t.Fatalf("token is not raw URL-safe base64: %v", err)
	}
	if len(decoded) != TokenEntropyBytes {
		t.Fatalf("decoded token bytes = %d, want %d", len(decoded), TokenEntropyBytes)
	}
}

func TestTokenDigestVerification(t *testing.T) {
	pepper := []byte("01234567890123456789012345678901")
	digest, err := DigestToken("gateway-token", pepper)
	if err != nil {
		t.Fatalf("DigestToken() error = %v", err)
	}

	match, err := VerifyTokenDigest("gateway-token", pepper, digest)
	if err != nil || !match {
		t.Fatalf("VerifyTokenDigest() = %v, %v; want true, nil", match, err)
	}
	match, err = VerifyTokenDigest("different-token", pepper, digest)
	if err != nil || match {
		t.Fatalf("VerifyTokenDigest(different) = %v, %v; want false, nil", match, err)
	}
}

func TestTokenDigestRejectsInvalidInputs(t *testing.T) {
	if _, err := DigestToken("token", []byte("short")); !errors.Is(err, ErrWeakHMACKey) {
		t.Fatalf("DigestToken() error = %v, want ErrWeakHMACKey", err)
	}
	if _, err := VerifyTokenDigest("token", make([]byte, 32), "not-a-digest"); !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("VerifyTokenDigest() error = %v, want ErrInvalidDigest", err)
	}
}
