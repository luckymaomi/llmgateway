package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	envelopeMagic         = "LLME"
	envelopeFormatVersion = byte(1)
	envelopeHeaderBytes   = 10
	envelopeKeyBytes      = 32
	envelopeTagBytes      = 16
)

var (
	ErrEnvelopeConfiguration  = errors.New("invalid envelope configuration")
	ErrMalformedEnvelope      = errors.New("malformed encrypted envelope")
	ErrUnsupportedEnvelope    = errors.New("unsupported encrypted envelope format")
	ErrUnknownEnvelopeKey     = errors.New("unknown envelope key version")
	ErrEnvelopeAuthentication = errors.New("encrypted envelope authentication failed")
	ErrEnvelopeEncryption     = errors.New("encrypted envelope creation failed")
)

type EnvelopeErrorKind string

const (
	EnvelopeConfigurationError  EnvelopeErrorKind = "configuration"
	EnvelopeMalformedError      EnvelopeErrorKind = "malformed"
	EnvelopeUnsupportedError    EnvelopeErrorKind = "unsupported_format"
	EnvelopeUnknownKeyError     EnvelopeErrorKind = "unknown_key"
	EnvelopeAuthenticationError EnvelopeErrorKind = "authentication"
	EnvelopeEncryptionError     EnvelopeErrorKind = "encryption"
)

type EnvelopeError struct {
	Kind       EnvelopeErrorKind
	KeyVersion uint32
	Cause      error
}

func (e *EnvelopeError) Error() string {
	switch e.Kind {
	case EnvelopeConfigurationError:
		return "invalid envelope cipher configuration"
	case EnvelopeMalformedError:
		return "malformed encrypted envelope"
	case EnvelopeUnsupportedError:
		return "unsupported encrypted envelope format"
	case EnvelopeUnknownKeyError:
		return fmt.Sprintf("unknown envelope key version %d", e.KeyVersion)
	case EnvelopeAuthenticationError:
		return "encrypted envelope authentication failed"
	case EnvelopeEncryptionError:
		return "encrypted envelope creation failed"
	default:
		return "encrypted envelope operation failed"
	}
}

func (e *EnvelopeError) Unwrap() error {
	return e.Cause
}

func (e *EnvelopeError) Is(target error) bool {
	switch target {
	case ErrEnvelopeConfiguration:
		return e.Kind == EnvelopeConfigurationError
	case ErrMalformedEnvelope:
		return e.Kind == EnvelopeMalformedError
	case ErrUnsupportedEnvelope:
		return e.Kind == EnvelopeUnsupportedError
	case ErrUnknownEnvelopeKey:
		return e.Kind == EnvelopeUnknownKeyError
	case ErrEnvelopeAuthentication:
		return e.Kind == EnvelopeAuthenticationError
	case ErrEnvelopeEncryption:
		return e.Kind == EnvelopeEncryptionError
	default:
		return false
	}
}

type EnvelopeMetadata struct {
	FormatVersion byte
	KeyVersion    uint32
}

type EnvelopeCipher struct {
	activeKeyVersion uint32
	keys             map[uint32]cipher.AEAD
	random           io.Reader
}

func NewEnvelopeCipher(activeKeyVersion uint32, keys map[uint32][]byte) (*EnvelopeCipher, error) {
	if activeKeyVersion == 0 || len(keys) == 0 {
		return nil, envelopeError(EnvelopeConfigurationError, activeKeyVersion, nil)
	}

	aeads := make(map[uint32]cipher.AEAD, len(keys))
	for version, key := range keys {
		if version == 0 || len(key) != envelopeKeyBytes {
			return nil, envelopeError(EnvelopeConfigurationError, version, nil)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, envelopeError(EnvelopeConfigurationError, version, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, envelopeError(EnvelopeConfigurationError, version, err)
		}
		aeads[version] = aead
	}
	if _, ok := aeads[activeKeyVersion]; !ok {
		return nil, envelopeError(EnvelopeConfigurationError, activeKeyVersion, nil)
	}

	return &EnvelopeCipher{
		activeKeyVersion: activeKeyVersion,
		keys:             aeads,
		random:           rand.Reader,
	}, nil
}

func (c *EnvelopeCipher) Encrypt(plaintext, additionalData []byte) ([]byte, error) {
	aead, ok := c.keys[c.activeKeyVersion]
	if !ok {
		return nil, envelopeError(EnvelopeConfigurationError, c.activeKeyVersion, nil)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return nil, envelopeError(EnvelopeEncryptionError, c.activeKeyVersion, err)
	}
	header := makeEnvelopeHeader(c.activeKeyVersion, len(nonce))
	authenticatedData := envelopeAuthenticatedData(header, additionalData)
	ciphertext := aead.Seal(nil, nonce, plaintext, authenticatedData)

	envelope := make([]byte, 0, len(header)+len(nonce)+len(ciphertext))
	envelope = append(envelope, header...)
	envelope = append(envelope, nonce...)
	envelope = append(envelope, ciphertext...)
	return envelope, nil
}

func (c *EnvelopeCipher) Decrypt(envelope, additionalData []byte) ([]byte, error) {
	metadata, header, nonce, ciphertext, err := parseEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	aead, ok := c.keys[metadata.KeyVersion]
	if !ok {
		return nil, envelopeError(EnvelopeUnknownKeyError, metadata.KeyVersion, nil)
	}
	if len(nonce) != aead.NonceSize() || len(ciphertext) < aead.Overhead() {
		return nil, envelopeError(EnvelopeMalformedError, metadata.KeyVersion, nil)
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, envelopeAuthenticatedData(header, additionalData))
	if err != nil {
		return nil, envelopeError(EnvelopeAuthenticationError, metadata.KeyVersion, nil)
	}
	return plaintext, nil
}

// Metadata parses the routing header without authenticating the envelope.
// Decisions that affect plaintext or rotation must use Decrypt or
// NeedsReencryption with the original additional data.
func (c *EnvelopeCipher) Metadata(envelope []byte) (EnvelopeMetadata, error) {
	metadata, _, _, _, err := parseEnvelope(envelope)
	return metadata, err
}

// NeedsReencryption authenticates the complete envelope before trusting the
// stored key version. The same additional data used at encryption is required.
func (c *EnvelopeCipher) NeedsReencryption(envelope, additionalData []byte) (bool, error) {
	if _, err := c.Decrypt(envelope, additionalData); err != nil {
		return false, err
	}
	metadata, err := c.Metadata(envelope)
	if err != nil {
		return false, err
	}
	if _, ok := c.keys[metadata.KeyVersion]; !ok {
		return false, envelopeError(EnvelopeUnknownKeyError, metadata.KeyVersion, nil)
	}
	return metadata.KeyVersion != c.activeKeyVersion, nil
}

func makeEnvelopeHeader(keyVersion uint32, nonceBytes int) []byte {
	header := make([]byte, envelopeHeaderBytes)
	copy(header[:4], envelopeMagic)
	header[4] = envelopeFormatVersion
	binary.BigEndian.PutUint32(header[5:9], keyVersion)
	header[9] = byte(nonceBytes)
	return header
}

func parseEnvelope(envelope []byte) (EnvelopeMetadata, []byte, []byte, []byte, error) {
	if len(envelope) < envelopeHeaderBytes || string(envelope[:4]) != envelopeMagic {
		return EnvelopeMetadata{}, nil, nil, nil, envelopeError(EnvelopeMalformedError, 0, nil)
	}
	if envelope[4] != envelopeFormatVersion {
		return EnvelopeMetadata{}, nil, nil, nil, envelopeError(EnvelopeUnsupportedError, 0, nil)
	}

	metadata := EnvelopeMetadata{
		FormatVersion: envelope[4],
		KeyVersion:    binary.BigEndian.Uint32(envelope[5:9]),
	}
	nonceBytes := int(envelope[9])
	if metadata.KeyVersion == 0 || nonceBytes == 0 || len(envelope) < envelopeHeaderBytes+nonceBytes+envelopeTagBytes {
		return EnvelopeMetadata{}, nil, nil, nil, envelopeError(EnvelopeMalformedError, metadata.KeyVersion, nil)
	}
	header := envelope[:envelopeHeaderBytes]
	nonce := envelope[envelopeHeaderBytes : envelopeHeaderBytes+nonceBytes]
	ciphertext := envelope[envelopeHeaderBytes+nonceBytes:]
	return metadata, header, nonce, ciphertext, nil
}

func envelopeAuthenticatedData(header, additionalData []byte) []byte {
	authenticatedData := make([]byte, 0, len(header)+8+len(additionalData))
	authenticatedData = append(authenticatedData, header...)
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(additionalData)))
	authenticatedData = append(authenticatedData, size[:]...)
	authenticatedData = append(authenticatedData, additionalData...)
	return authenticatedData
}

func envelopeError(kind EnvelopeErrorKind, keyVersion uint32, cause error) error {
	return &EnvelopeError{Kind: kind, KeyVersion: keyVersion, Cause: cause}
}
