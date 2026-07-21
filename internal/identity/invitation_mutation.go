package identity

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func newInvitationMutation(request MutationRequest, expiresAt time.Time) (InvitationMutation, error) {
	if request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return InvitationMutation{}, ErrInvalidInput
	}
	payload := struct {
		ExpiresAt string `json:"expires_at"`
	}{
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return InvitationMutation{}, ErrInvalidInput
	}
	fingerprint := sha256.Sum256(encoded)
	return InvitationMutation{
		IdempotencyKey:     request.IdempotencyKey,
		RequestFingerprint: append([]byte(nil), fingerprint[:]...),
		RequestID:          request.RequestID,
	}, nil
}
