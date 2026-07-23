package registry

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

func mutationFingerprint(request MutationRequest, action string, payload any) (Mutation, error) {
	if request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return Mutation{}, ErrInvalidInput
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Mutation{}, ErrInvalidInput
	}
	digest := sha256.Sum256(encoded)
	return Mutation{Action: action, IdempotencyKey: request.IdempotencyKey, RequestFingerprint: digest[:], RequestID: request.RequestID}, nil
}

func resourcePoolMutation(request MutationRequest, action string, input any) (Mutation, error) {
	return mutationFingerprint(request, action, input)
}

func credentialCreateMutation(request MutationRequest, input NewCredential, secret string) (Mutation, error) {
	payload := struct {
		ResourcePoolID   uuid.UUID                      `json:"resource_pool_id"`
		Name             string                         `json:"name"`
		RPMLimit         *int32                         `json:"rpm_limit"`
		TPMLimit         *int64                         `json:"tpm_limit"`
		ConcurrencyLimit *int32                         `json:"concurrency_limit"`
		ModelBindings    []credentialBindingFingerprint `json:"model_bindings"`
		SecretDigest     [32]byte                       `json:"secret_digest"`
	}{input.ResourcePoolID, input.Name, input.RPMLimit, input.TPMLimit, input.ConcurrencyLimit, sortedCredentialBindings(input.ModelBindings), sha256.Sum256([]byte(secret))}
	return mutationFingerprint(request, "credential.create", payload)
}

func credentialUpdateMutation(request MutationRequest, input CredentialChange, secret string) (Mutation, error) {
	var secretDigest *[32]byte
	if input.ReplaceSecret {
		digest := sha256.Sum256([]byte(secret))
		secretDigest = &digest
	}
	payload := struct {
		ID                uuid.UUID                      `json:"id"`
		Name              string                         `json:"name"`
		RPMLimit          *int32                         `json:"rpm_limit"`
		TPMLimit          *int64                         `json:"tpm_limit"`
		ConcurrencyLimit  *int32                         `json:"concurrency_limit"`
		ModelBindings     []credentialBindingFingerprint `json:"model_bindings"`
		ExpectedUpdatedAt time.Time                      `json:"expected_updated_at"`
		SecretDigest      *[32]byte                      `json:"secret_digest,omitempty"`
	}{input.ID, input.Name, input.RPMLimit, input.TPMLimit, input.ConcurrencyLimit, sortedCredentialBindings(input.ModelBindings), input.ExpectedUpdatedAt.UTC().Truncate(time.Microsecond), secretDigest}
	return mutationFingerprint(request, "credential.update", payload)
}

type credentialBindingFingerprint struct {
	ModelID  uuid.UUID `json:"model_id"`
	Priority int32     `json:"priority"`
	Weight   int32     `json:"weight"`
}

func sortedCredentialBindings(bindings []CredentialModelBinding) []credentialBindingFingerprint {
	values := make([]credentialBindingFingerprint, len(bindings))
	for index, binding := range bindings {
		values[index] = credentialBindingFingerprint{ModelID: binding.ModelID, Priority: binding.Priority, Weight: binding.Weight}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ModelID.String() < values[j].ModelID.String() })
	return values
}
