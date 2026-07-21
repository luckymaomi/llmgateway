package identity

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

func newGatewayKeyMutation(request MutationRequest, userID uuid.UUID, name string, modelIDs []uuid.UUID, expiresAt *time.Time, replacesKeyID *uuid.UUID) (GatewayKeyMutation, error) {
	if request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return GatewayKeyMutation{}, ErrInvalidInput
	}
	var expiry *string
	if expiresAt != nil {
		formatted := expiresAt.UTC().Format(time.RFC3339Nano)
		expiry = &formatted
	}
	payload := struct {
		UserID        uuid.UUID   `json:"user_id"`
		Name          string      `json:"name"`
		ModelIDs      []uuid.UUID `json:"model_ids"`
		ExpiresAt     *string     `json:"expires_at"`
		ReplacesKeyID *uuid.UUID  `json:"replaces_key_id"`
	}{UserID: userID, Name: name, ModelIDs: modelIDs, ExpiresAt: expiry, ReplacesKeyID: replacesKeyID}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return GatewayKeyMutation{}, ErrInvalidInput
	}
	fingerprint := sha256.Sum256(encoded)
	return GatewayKeyMutation{
		IdempotencyKey: request.IdempotencyKey, RequestFingerprint: append([]byte(nil), fingerprint[:]...), RequestID: request.RequestID,
	}, nil
}
