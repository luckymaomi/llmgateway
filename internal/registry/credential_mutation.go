package registry

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

func newCredentialMutation(request MutationRequest, input NewCredential, secret string) (CredentialMutation, error) {
	if request.IdempotencyKey == [16]byte{} || request.RequestID == "" || len(request.RequestID) > 128 {
		return CredentialMutation{}, ErrInvalidInput
	}
	payload := struct {
		Action           CredentialMutationAction       `json:"action"`
		ProviderID       string                         `json:"provider_id"`
		Name             string                         `json:"name"`
		ResourceDomain   ResourceDomain                 `json:"resource_domain"`
		RPMLimit         *int32                         `json:"rpm_limit"`
		TPMLimit         *int64                         `json:"tpm_limit"`
		ConcurrencyLimit *int32                         `json:"concurrency_limit"`
		ModelBindings    []credentialBindingFingerprint `json:"model_bindings"`
		SecretDigest     [32]byte                       `json:"secret_digest"`
	}{
		Action: CredentialMutationCreate, ProviderID: input.ProviderID.String(), Name: input.Name, ResourceDomain: input.ResourceDomain,
		RPMLimit: input.RPMLimit, TPMLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit,
		ModelBindings: sortedCredentialBindings(input.ModelBindings), SecretDigest: sha256.Sum256([]byte(secret)),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return CredentialMutation{}, ErrInvalidInput
	}
	fingerprint := sha256.Sum256(encoded)
	return CredentialMutation{
		Action: CredentialMutationCreate, IdempotencyKey: request.IdempotencyKey, RequestFingerprint: append([]byte(nil), fingerprint[:]...), RequestID: request.RequestID,
	}, nil
}

func updateCredentialMutation(request MutationRequest, input CredentialChange, secret string) (CredentialMutation, error) {
	var secretDigest *[32]byte
	if input.ReplaceSecret {
		digest := sha256.Sum256([]byte(secret))
		secretDigest = &digest
	}
	payload := struct {
		Action            CredentialMutationAction       `json:"action"`
		CredentialID      string                         `json:"credential_id"`
		Name              string                         `json:"name"`
		ResourceDomain    ResourceDomain                 `json:"resource_domain"`
		RPMLimit          *int32                         `json:"rpm_limit"`
		TPMLimit          *int64                         `json:"tpm_limit"`
		ConcurrencyLimit  *int32                         `json:"concurrency_limit"`
		ModelBindings     []credentialBindingFingerprint `json:"model_bindings"`
		ExpectedUpdatedAt time.Time                      `json:"expected_updated_at"`
		SecretDigest      *[32]byte                      `json:"secret_digest,omitempty"`
	}{
		Action: CredentialMutationUpdate, CredentialID: input.ID.String(), Name: input.Name,
		ResourceDomain: input.ResourceDomain, RPMLimit: input.RPMLimit, TPMLimit: input.TPMLimit,
		ConcurrencyLimit: input.ConcurrencyLimit,
		ModelBindings:    sortedCredentialBindings(input.ModelBindings), ExpectedUpdatedAt: input.ExpectedUpdatedAt.UTC().Truncate(time.Microsecond), SecretDigest: secretDigest,
	}
	return credentialMutationFingerprint(request, CredentialMutationUpdate, payload)
}

func statusCredentialMutation(request MutationRequest, credentialID uuid.UUID, status CredentialStatus, expectedUpdatedAt time.Time) (CredentialMutation, error) {
	payload := struct {
		Action            CredentialMutationAction `json:"action"`
		CredentialID      string                   `json:"credential_id"`
		Status            CredentialStatus         `json:"status"`
		ExpectedUpdatedAt time.Time                `json:"expected_updated_at"`
	}{CredentialMutationStatus, credentialID.String(), status, expectedUpdatedAt.UTC().Truncate(time.Microsecond)}
	return credentialMutationFingerprint(request, CredentialMutationStatus, payload)
}

func credentialMutationFingerprint(request MutationRequest, action CredentialMutationAction, payload any) (CredentialMutation, error) {
	if request.IdempotencyKey == uuid.Nil || request.RequestID == "" || len(request.RequestID) > 128 {
		return CredentialMutation{}, ErrInvalidInput
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return CredentialMutation{}, ErrInvalidInput
	}
	fingerprint := sha256.Sum256(encoded)
	return CredentialMutation{Action: action, IdempotencyKey: request.IdempotencyKey, RequestFingerprint: append([]byte(nil), fingerprint[:]...), RequestID: request.RequestID}, nil
}

type credentialBindingFingerprint struct {
	ModelID  string `json:"model_id"`
	Priority int32  `json:"priority"`
	Weight   int32  `json:"weight"`
}

func sortedCredentialBindings(bindings []CredentialModelBinding) []credentialBindingFingerprint {
	values := make([]credentialBindingFingerprint, len(bindings))
	for index, binding := range bindings {
		values[index] = credentialBindingFingerprint{ModelID: binding.ModelID.String(), Priority: binding.Priority, Weight: binding.Weight}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ModelID < values[j].ModelID })
	return values
}
