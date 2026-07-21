package configuration

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/google/uuid"
)

func newMutation(action MutationAction, request MutationRequest, revisionID uuid.UUID, expectedActiveVersion *int64) (Mutation, error) {
	if request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return Mutation{}, ErrInvalidInput
	}
	payload := struct {
		Action                MutationAction `json:"action"`
		RevisionID            uuid.UUID      `json:"revision_id,omitempty"`
		ExpectedActiveVersion *int64         `json:"expected_active_version,omitempty"`
	}{Action: action, RevisionID: revisionID, ExpectedActiveVersion: expectedActiveVersion}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Mutation{}, ErrInvalidInput
	}
	fingerprint := sha256.Sum256(encoded)
	return Mutation{Action: action, IdempotencyKey: request.IdempotencyKey, RequestFingerprint: fingerprint[:], RequestID: request.RequestID}, nil
}
