package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	minimumPasswordMemoryKiB    uint32 = 19 * 1024
	maximumPasswordMemoryKiB    uint32 = 256 * 1024
	maximumPasswordIterations   uint32 = 10
	maximumPasswordParallelism  uint8  = 16
	maximumPasswordOutputBytes  uint32 = 64
	maximumEncodedPasswordBytes        = 1024
)

var (
	ErrInvalidPasswordParameters = errors.New("invalid password hashing parameters")
	ErrInvalidPasswordHash       = errors.New("invalid password hash")
	ErrUnsupportedPasswordHash   = errors.New("unsupported password hash")
)

// PasswordParameters is persisted alongside each password hash so stronger
// settings can be introduced without invalidating existing credentials.
type PasswordParameters struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   uint32
	OutputBytes uint32
}

// RecommendedPasswordParameters returns the current production baseline.
func RecommendedPasswordParameters() PasswordParameters {
	return PasswordParameters{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltBytes:   16,
		OutputBytes: 32,
	}
}

func (p PasswordParameters) Validate() error {
	if p.MemoryKiB < minimumPasswordMemoryKiB || p.MemoryKiB > maximumPasswordMemoryKiB {
		return fmt.Errorf("%w: memory must be between %d and %d KiB", ErrInvalidPasswordParameters, minimumPasswordMemoryKiB, maximumPasswordMemoryKiB)
	}
	if p.Iterations < 2 || p.Iterations > maximumPasswordIterations {
		return fmt.Errorf("%w: iterations must be between 2 and %d", ErrInvalidPasswordParameters, maximumPasswordIterations)
	}
	if p.Parallelism == 0 || p.Parallelism > maximumPasswordParallelism {
		return fmt.Errorf("%w: parallelism must be between 1 and %d", ErrInvalidPasswordParameters, maximumPasswordParallelism)
	}
	if p.SaltBytes < 16 || p.SaltBytes > maximumPasswordOutputBytes {
		return fmt.Errorf("%w: salt must be between 16 and %d bytes", ErrInvalidPasswordParameters, maximumPasswordOutputBytes)
	}
	if p.OutputBytes < 32 || p.OutputBytes > maximumPasswordOutputBytes {
		return fmt.Errorf("%w: output must be between 32 and %d bytes", ErrInvalidPasswordParameters, maximumPasswordOutputBytes)
	}
	return nil
}

type PasswordVerification struct {
	Match        bool
	NeedsUpgrade bool
	Parameters   PasswordParameters
}

func HashPassword(password string, parameters PasswordParameters) (string, error) {
	if err := parameters.Validate(); err != nil {
		return "", err
	}

	salt := make([]byte, parameters.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, parameters.Iterations, parameters.MemoryKiB, parameters.Parallelism, parameters.OutputBytes)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		parameters.MemoryKiB,
		parameters.Iterations,
		parameters.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(password, encodedHash string, target PasswordParameters) (PasswordVerification, error) {
	if err := target.Validate(); err != nil {
		return PasswordVerification{}, err
	}

	stored, salt, expected, err := parsePasswordHash(encodedHash)
	if err != nil {
		return PasswordVerification{}, err
	}
	actual := argon2.IDKey([]byte(password), salt, stored.Iterations, stored.MemoryKiB, stored.Parallelism, stored.OutputBytes)
	match := subtle.ConstantTimeCompare(actual, expected) == 1

	return PasswordVerification{
		Match:        match,
		NeedsUpgrade: match && passwordParametersNeedUpgrade(stored, target),
		Parameters:   stored,
	}, nil
}

func parsePasswordHash(encodedHash string) (PasswordParameters, []byte, []byte, error) {
	if len(encodedHash) == 0 || len(encodedHash) > maximumEncodedPasswordBytes {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}
	if parts[1] != "argon2id" {
		return PasswordParameters{}, nil, nil, ErrUnsupportedPasswordHash
	}

	version, err := parseNamedUint(parts[2], "v", 8)
	if err != nil {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}
	if version != argon2.Version {
		return PasswordParameters{}, nil, nil, ErrUnsupportedPasswordHash
	}

	parameterParts := strings.Split(parts[3], ",")
	if len(parameterParts) != 3 {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}
	memoryKiB, err := parseNamedUint(parameterParts[0], "m", 32)
	if err != nil {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}
	iterations, err := parseNamedUint(parameterParts[1], "t", 32)
	if err != nil {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}
	parallelism, err := parseNamedUint(parameterParts[2], "p", 8)
	if err != nil {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return PasswordParameters{}, nil, nil, ErrInvalidPasswordHash
	}

	stored := PasswordParameters{
		MemoryKiB:   uint32(memoryKiB),
		Iterations:  uint32(iterations),
		Parallelism: uint8(parallelism),
		SaltBytes:   uint32(len(salt)),
		OutputBytes: uint32(len(expected)),
	}
	if err := validateStoredPasswordParameters(stored); err != nil {
		return PasswordParameters{}, nil, nil, err
	}
	return stored, salt, expected, nil
}

func validateStoredPasswordParameters(parameters PasswordParameters) error {
	// Stored hashes may be weaker than the current target so they can be
	// authenticated once and upgraded. Upper bounds prevent corrupted records
	// from forcing unbounded CPU or memory work.
	if parameters.MemoryKiB < 8*1024 || parameters.MemoryKiB > maximumPasswordMemoryKiB ||
		parameters.Iterations == 0 || parameters.Iterations > maximumPasswordIterations ||
		parameters.Parallelism == 0 || parameters.Parallelism > maximumPasswordParallelism ||
		parameters.SaltBytes < 8 || parameters.SaltBytes > maximumPasswordOutputBytes ||
		parameters.OutputBytes < 16 || parameters.OutputBytes > maximumPasswordOutputBytes {
		return ErrInvalidPasswordHash
	}
	return nil
}

func parseNamedUint(value, name string, bitSize int) (uint64, error) {
	prefix := name + "="
	if !strings.HasPrefix(value, prefix) {
		return 0, ErrInvalidPasswordHash
	}
	return strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, bitSize)
}

func passwordParametersNeedUpgrade(stored, target PasswordParameters) bool {
	return stored.MemoryKiB < target.MemoryKiB ||
		stored.Iterations < target.Iterations ||
		stored.Parallelism < target.Parallelism ||
		stored.SaltBytes < target.SaltBytes ||
		stored.OutputBytes < target.OutputBytes
}
