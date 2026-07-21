package identity

import (
	"strings"

	"github.com/luckymaomi/llmgateway/internal/security"
)

func HashRecoveryPassword(password string) (string, error) {
	return hashPassword(password, security.RecommendedPasswordParameters())
}

func hashPassword(password string, parameters security.PasswordParameters) (string, error) {
	if len(password) < minimumPasswordBytes || len(password) > maximumPasswordBytes || strings.ContainsRune(password, '\x00') {
		return "", ErrInvalidInput
	}
	passwordHash, err := security.HashPassword(password, parameters)
	if err != nil {
		return "", err
	}
	return passwordHash, nil
}
