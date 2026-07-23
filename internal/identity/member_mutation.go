package identity

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/google/uuid"
)

func newMemberMutation(action MemberMutationAction, request MutationRequest, payload any) (MemberMutation, error) {
	if action == "" || request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return MemberMutation{}, ErrInvalidInput
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return MemberMutation{}, ErrInvalidInput
	}
	fingerprint := sha256.Sum256(encoded)
	return MemberMutation{
		Action: action, IdempotencyKey: request.IdempotencyKey,
		RequestFingerprint: append([]byte(nil), fingerprint[:]...), RequestID: request.RequestID,
	}, nil
}
