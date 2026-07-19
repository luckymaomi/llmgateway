package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	TokenEntropyBytes   = 32
	MinimumHMACKeyBytes = 32
)

var (
	ErrWeakHMACKey     = errors.New("HMAC key must contain at least 32 bytes")
	ErrInvalidDigest   = errors.New("invalid HMAC-SHA256 digest")
	ErrTokenGeneration = errors.New("token generation failed")
)

type HMACDigest [sha256.Size]byte

func GenerateToken() (string, error) {
	randomBytes := make([]byte, TokenEntropyBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTokenGeneration, err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func HMACSHA256(key, value []byte) (HMACDigest, error) {
	if len(key) < MinimumHMACKeyBytes {
		return HMACDigest{}, ErrWeakHMACKey
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	var digest HMACDigest
	copy(digest[:], mac.Sum(nil))
	return digest, nil
}

func VerifyHMACSHA256(key, value []byte, expected HMACDigest) (bool, error) {
	actual, err := HMACSHA256(key, value)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1, nil
}

func (digest HMACDigest) Encode() string {
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func ParseHMACDigest(encoded string) (HMACDigest, error) {
	if len(encoded) != base64.RawURLEncoding.EncodedLen(sha256.Size) {
		return HMACDigest{}, ErrInvalidDigest
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return HMACDigest{}, ErrInvalidDigest
	}
	var digest HMACDigest
	copy(digest[:], decoded)
	return digest, nil
}

func DigestToken(token string, pepper []byte) (string, error) {
	digest, err := HMACSHA256(pepper, []byte(token))
	if err != nil {
		return "", err
	}
	return digest.Encode(), nil
}

func VerifyTokenDigest(token string, pepper []byte, encodedDigest string) (bool, error) {
	digest, err := ParseHMACDigest(encodedDigest)
	if err != nil {
		return false, err
	}
	return VerifyHMACSHA256(pepper, []byte(token), digest)
}
