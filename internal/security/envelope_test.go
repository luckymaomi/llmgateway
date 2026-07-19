package security

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func envelopeTestKey(value byte) []byte {
	return bytes.Repeat([]byte{value}, envelopeKeyBytes)
}

func TestEnvelopeEncryptDecryptAndRotation(t *testing.T) {
	oldCipher, err := NewEnvelopeCipher(1, map[uint32][]byte{1: envelopeTestKey(1)})
	if err != nil {
		t.Fatalf("NewEnvelopeCipher(old) error = %v", err)
	}
	additionalData := []byte("credential:provider-account-17")
	encrypted, err := oldCipher.Encrypt([]byte("upstream-secret"), additionalData)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	rotatedCipher, err := NewEnvelopeCipher(2, map[uint32][]byte{
		1: envelopeTestKey(1),
		2: envelopeTestKey(2),
	})
	if err != nil {
		t.Fatalf("NewEnvelopeCipher(rotated) error = %v", err)
	}
	needsReencryption, err := rotatedCipher.NeedsReencryption(encrypted, additionalData)
	if err != nil || !needsReencryption {
		t.Fatalf("NeedsReencryption() = %v, %v; want true, nil", needsReencryption, err)
	}
	plaintext, err := rotatedCipher.Decrypt(encrypted, additionalData)
	if err != nil {
		t.Fatalf("Decrypt(old key) error = %v", err)
	}
	if string(plaintext) != "upstream-secret" {
		t.Fatalf("Decrypt(old key) = %q", plaintext)
	}

	current, err := rotatedCipher.Encrypt(plaintext, additionalData)
	if err != nil {
		t.Fatalf("Encrypt(current key) error = %v", err)
	}
	needsReencryption, err = rotatedCipher.NeedsReencryption(current, additionalData)
	if err != nil || needsReencryption {
		t.Fatalf("NeedsReencryption(current) = %v, %v; want false, nil", needsReencryption, err)
	}
	metadata, err := rotatedCipher.Metadata(current)
	if err != nil || metadata.KeyVersion != 2 || metadata.FormatVersion != envelopeFormatVersion {
		t.Fatalf("Metadata() = %+v, %v", metadata, err)
	}
}

func TestEnvelopeAuthenticatesCiphertextHeaderAndAdditionalData(t *testing.T) {
	cipher, err := NewEnvelopeCipher(1, map[uint32][]byte{
		1: envelopeTestKey(1),
		2: envelopeTestKey(2),
	})
	if err != nil {
		t.Fatalf("NewEnvelopeCipher() error = %v", err)
	}
	encrypted, err := cipher.Encrypt([]byte("secret"), []byte("record-a"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	tamperedCiphertext := bytes.Clone(encrypted)
	tamperedCiphertext[len(tamperedCiphertext)-1] ^= 1
	assertEnvelopeAuthenticationError(t, cipher, tamperedCiphertext, []byte("record-a"))
	assertEnvelopeAuthenticationError(t, cipher, encrypted, []byte("record-b"))

	tamperedHeader := bytes.Clone(encrypted)
	binary.BigEndian.PutUint32(tamperedHeader[5:9], 2)
	assertEnvelopeAuthenticationError(t, cipher, tamperedHeader, []byte("record-a"))
}

func TestEnvelopeReportsActionableFormatAndKeyErrors(t *testing.T) {
	cipher, err := NewEnvelopeCipher(1, map[uint32][]byte{1: envelopeTestKey(1)})
	if err != nil {
		t.Fatalf("NewEnvelopeCipher() error = %v", err)
	}
	encrypted, err := cipher.Encrypt([]byte("secret"), nil)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	unknownKey := bytes.Clone(encrypted)
	binary.BigEndian.PutUint32(unknownKey[5:9], 9)
	if _, err := cipher.Decrypt(unknownKey, nil); !errors.Is(err, ErrUnknownEnvelopeKey) {
		t.Fatalf("Decrypt(unknown key) error = %v, want ErrUnknownEnvelopeKey", err)
	}
	unsupported := bytes.Clone(encrypted)
	unsupported[4]++
	if _, err := cipher.Decrypt(unsupported, nil); !errors.Is(err, ErrUnsupportedEnvelope) {
		t.Fatalf("Decrypt(unsupported) error = %v, want ErrUnsupportedEnvelope", err)
	}
	if _, err := cipher.Decrypt([]byte("short"), nil); !errors.Is(err, ErrMalformedEnvelope) {
		t.Fatalf("Decrypt(malformed) error = %v, want ErrMalformedEnvelope", err)
	}
}

func TestEnvelopeRequiresAES256Keys(t *testing.T) {
	_, err := NewEnvelopeCipher(1, map[uint32][]byte{1: bytes.Repeat([]byte{1}, envelopeKeyBytes-1)})
	if !errors.Is(err, ErrEnvelopeConfiguration) {
		t.Fatalf("NewEnvelopeCipher(short key) error = %v, want ErrEnvelopeConfiguration", err)
	}
}

func assertEnvelopeAuthenticationError(t *testing.T, cipher *EnvelopeCipher, encrypted, additionalData []byte) {
	t.Helper()
	_, err := cipher.Decrypt(encrypted, additionalData)
	if !errors.Is(err, ErrEnvelopeAuthentication) {
		t.Fatalf("Decrypt(tampered) error = %v, want ErrEnvelopeAuthentication", err)
	}
	var typed *EnvelopeError
	if !errors.As(err, &typed) || typed.Kind != EnvelopeAuthenticationError {
		t.Fatalf("Decrypt(tampered) typed error = %#v", typed)
	}
}
