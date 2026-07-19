package security

import (
	"errors"
	"strings"
	"testing"
)

func testPasswordParameters() PasswordParameters {
	return PasswordParameters{
		MemoryKiB:   minimumPasswordMemoryKiB,
		Iterations:  2,
		Parallelism: 1,
		SaltBytes:   16,
		OutputBytes: 32,
	}
}

func TestPasswordHashVerificationAndUpgrade(t *testing.T) {
	parameters := testPasswordParameters()
	encoded, err := HashPassword("correct horse battery staple", parameters)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	second, err := HashPassword("correct horse battery staple", parameters)
	if err != nil {
		t.Fatalf("second HashPassword() error = %v", err)
	}
	if encoded == second {
		t.Fatal("independent password hashes must use independent salts")
	}

	verification, err := VerifyPassword("correct horse battery staple", encoded, parameters)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !verification.Match || verification.NeedsUpgrade {
		t.Fatalf("VerifyPassword() = %+v, want match without upgrade", verification)
	}

	verification, err = VerifyPassword("wrong password", encoded, parameters)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong) error = %v", err)
	}
	if verification.Match || verification.NeedsUpgrade {
		t.Fatalf("VerifyPassword(wrong) = %+v, want mismatch", verification)
	}

	stronger := parameters
	stronger.Iterations++
	verification, err = VerifyPassword("correct horse battery staple", encoded, stronger)
	if err != nil {
		t.Fatalf("VerifyPassword(upgrade) error = %v", err)
	}
	if !verification.Match || !verification.NeedsUpgrade {
		t.Fatalf("VerifyPassword(upgrade) = %+v, want matched upgrade", verification)
	}
}

func TestPasswordHashRejectsUnsafeStoredCost(t *testing.T) {
	parameters := testPasswordParameters()
	encoded, err := HashPassword("password", parameters)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	encoded = strings.Replace(encoded, "m=19456", "m=999999999", 1)

	_, err = VerifyPassword("password", encoded, parameters)
	if !errors.Is(err, ErrInvalidPasswordHash) {
		t.Fatalf("VerifyPassword() error = %v, want ErrInvalidPasswordHash", err)
	}
}

func TestPasswordParametersEnforceProductionFloor(t *testing.T) {
	parameters := testPasswordParameters()
	parameters.MemoryKiB--
	if !errors.Is(parameters.Validate(), ErrInvalidPasswordParameters) {
		t.Fatal("weak memory setting must fail validation")
	}
}
