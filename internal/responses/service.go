package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/security"
)

type Service struct {
	repository Repository
	envelope   *security.EnvelopeCipher
	observer   Observer
}

func NewService(repository Repository, envelope *security.EnvelopeCipher) (*Service, error) {
	if repository == nil || envelope == nil {
		return nil, ErrInvalidInput
	}
	return &Service{repository: repository, envelope: envelope, observer: noopObserver{}}, nil
}

func (s *Service) WithObserver(observer Observer) *Service {
	if observer != nil {
		s.observer = observer
	}
	return s
}

type noopObserver struct{}

func (noopObserver) BackgroundResponse(string) {}

func (s *Service) Begin(ctx context.Context, responseID, requestID, gatewayKeyID uuid.UUID, previousResponseID *uuid.UUID, input json.RawMessage) error {
	record, err := s.encryptRecord(Record{ID: responseID, RequestID: &requestID, GatewayKeyID: gatewayKeyID, PreviousResponseID: previousResponseID, Status: StatusInProgress, Input: input})
	if err != nil {
		return err
	}
	_, err = s.repository.Create(ctx, record)
	return err
}

func (s *Service) SaveCompleted(ctx context.Context, responseID, requestID, gatewayKeyID uuid.UUID, previousResponseID *uuid.UUID, input, output json.RawMessage) error {
	record, err := s.encryptRecord(Record{ID: responseID, RequestID: &requestID, GatewayKeyID: gatewayKeyID, PreviousResponseID: previousResponseID, Status: StatusCompleted, Input: input, Output: output})
	if err != nil {
		return err
	}
	_, err = s.repository.CreateCompleted(ctx, record)
	return err
}

func (s *Service) Enqueue(ctx context.Context, responseID, gatewayKeyID uuid.UUID, previousResponseID *uuid.UUID, idempotencyKey *string, requestDigest []byte, input, request json.RawMessage) (Record, error) {
	record, err := s.encryptRecord(Record{ID: responseID, GatewayKeyID: gatewayKeyID, PreviousResponseID: previousResponseID, IdempotencyKey: idempotencyKey, RequestDigest: requestDigest, Status: StatusQueued, Background: true, Input: input, Request: request})
	if err != nil {
		return Record{}, err
	}
	created, err := s.repository.CreateBackground(ctx, record)
	if err != nil {
		return Record{}, err
	}
	s.observer.BackgroundResponse("queued")
	return s.decryptRecord(created)
}

func (s *Service) ClaimNext(ctx context.Context, executionID uuid.UUID, staleBefore time.Time) (Claim, Record, error) {
	if executionID == uuid.Nil || staleBefore.IsZero() {
		return Claim{}, Record{}, ErrInvalidInput
	}
	encrypted, err := s.repository.ClaimBackground(ctx, executionID, staleBefore.UTC())
	if err != nil {
		return Claim{}, Record{}, err
	}
	record, err := s.decryptRecord(encrypted)
	if err != nil {
		return Claim{}, Record{}, err
	}
	claim := Claim{ResponseID: record.ID, ExecutionID: executionID, Generation: encrypted.ExecutionGeneration}
	if !claim.Valid() || !record.Background || len(record.Request) == 0 {
		return Claim{}, Record{}, ErrConflict
	}
	s.observer.BackgroundResponse("claimed")
	return claim, record, nil
}

func (s *Service) Heartbeat(ctx context.Context, claim Claim) error {
	if !claim.Valid() {
		return ErrInvalidInput
	}
	return s.repository.HeartbeatBackground(ctx, claim)
}

func (s *Service) LinkRequest(ctx context.Context, claim Claim, requestID uuid.UUID) error {
	if !claim.Valid() || requestID == uuid.Nil || requestID != claim.ResponseID {
		return ErrInvalidInput
	}
	return s.repository.LinkBackgroundRequest(ctx, claim, requestID)
}

func (s *Service) StageOutput(ctx context.Context, claim Claim, requestID uuid.UUID, output json.RawMessage) error {
	if !claim.Valid() || requestID == uuid.Nil {
		return ErrInvalidInput
	}
	encrypted, err := s.encryptField(claim.ResponseID, "output", output)
	if err != nil {
		return err
	}
	return s.repository.StageBackgroundOutput(ctx, claim, requestID, encrypted)
}

func (s *Service) CompleteClaim(ctx context.Context, claim Claim, requestID uuid.UUID) error {
	if !claim.Valid() || requestID == uuid.Nil {
		return ErrInvalidInput
	}
	err := s.repository.CompleteBackground(ctx, claim, requestID)
	if err == nil {
		s.observer.BackgroundResponse("completed")
	}
	return err
}

func (s *Service) TerminateClaim(ctx context.Context, claim Claim, requestID *uuid.UUID, status Status, responseError json.RawMessage) error {
	if !claim.Valid() || status != StatusFailed && status != StatusCanceled && status != StatusUncertain {
		return ErrInvalidInput
	}
	encrypted, err := s.encryptField(claim.ResponseID, "error", responseError)
	if err != nil {
		return err
	}
	err = s.repository.TerminateBackground(ctx, claim, requestID, status, encrypted)
	if err == nil {
		s.observer.BackgroundResponse(string(status))
	}
	return err
}

func (s *Service) RecoverOnce(ctx context.Context, batchSize int32) (int, error) {
	if batchSize < 1 || batchSize > 1000 {
		return 0, ErrInvalidInput
	}
	recoveries, err := s.repository.ListBackgroundRecoveries(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	completed := 0
	var recoveryErrors []error
	for _, recovery := range recoveries {
		if err := s.repository.AttachBackgroundRequest(ctx, recovery.ResponseID); err != nil {
			if recoveryOwnershipLost(err) {
				continue
			}
			recoveryErrors = append(recoveryErrors, err)
			continue
		}
		status := Status("")
		switch recovery.RequestStatus {
		case "completed":
			if recovery.HasOutput {
				status = StatusCompleted
			} else {
				status = StatusFailed
			}
		case "failed":
			status = StatusFailed
		case "canceled":
			status = StatusCanceled
		case "uncertain":
			status = StatusUncertain
		}
		if status == "" {
			continue
		}
		var encryptedError []byte
		if status != StatusCompleted {
			body, _ := json.Marshal(map[string]any{"code": valueOr(recovery.ErrorKind, "background_execution_failed"), "message": valueOr(recovery.ErrorDetail, "background response did not complete")})
			encryptedError, err = s.encryptField(recovery.ResponseID, "error", body)
			if err != nil {
				recoveryErrors = append(recoveryErrors, err)
				continue
			}
		}
		if err := s.repository.FinalizeRecoveredBackground(ctx, recovery.ResponseID, status, encryptedError); err != nil {
			if recoveryOwnershipLost(err) {
				continue
			}
			recoveryErrors = append(recoveryErrors, err)
			continue
		}
		s.observer.BackgroundResponse("recovered_" + string(status))
		completed++
	}
	return completed, errors.Join(recoveryErrors...)
}

func recoveryOwnershipLost(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound)
}

func (s *Service) Complete(ctx context.Context, responseID uuid.UUID, output json.RawMessage) error {
	encrypted, err := s.encryptField(responseID, "output", output)
	if err != nil {
		return err
	}
	_, err = s.repository.Complete(ctx, responseID, encrypted)
	return err
}

func (s *Service) Fail(ctx context.Context, responseID uuid.UUID, responseError json.RawMessage) error {
	encrypted, err := s.encryptField(responseID, "error", responseError)
	if err != nil {
		return err
	}
	_, err = s.repository.Fail(ctx, responseID, encrypted)
	return err
}

func (s *Service) Get(ctx context.Context, responseID, gatewayKeyID uuid.UUID) (Record, error) {
	if responseID == uuid.Nil || gatewayKeyID == uuid.Nil {
		return Record{}, ErrInvalidInput
	}
	encrypted, err := s.repository.Get(ctx, responseID, gatewayKeyID)
	if err != nil {
		return Record{}, err
	}
	return s.decryptRecord(encrypted)
}

func (s *Service) Delete(ctx context.Context, responseID, gatewayKeyID uuid.UUID) error {
	if responseID == uuid.Nil || gatewayKeyID == uuid.Nil {
		return ErrInvalidInput
	}
	return s.repository.Delete(ctx, responseID, gatewayKeyID)
}

func (s *Service) RequestCancellation(ctx context.Context, responseID, gatewayKeyID uuid.UUID) error {
	if responseID == uuid.Nil || gatewayKeyID == uuid.Nil {
		return ErrInvalidInput
	}
	record, err := s.repository.Get(ctx, responseID, gatewayKeyID)
	if err != nil {
		return err
	}
	if !record.Background {
		return ErrNotCancelable
	}
	_, err = s.repository.RequestCancellation(ctx, responseID, gatewayKeyID)
	return err
}

func (s *Service) encryptRecord(record Record) (EncryptedRecord, error) {
	if record.ID == uuid.Nil || record.GatewayKeyID == uuid.Nil || !json.Valid(record.Input) || record.RequestID != nil && *record.RequestID == uuid.Nil || record.Background && !json.Valid(record.Request) || record.IdempotencyKey != nil && (len(record.RequestDigest) != 32 || *record.IdempotencyKey == "") || record.IdempotencyKey == nil && len(record.RequestDigest) != 0 {
		return EncryptedRecord{}, ErrInvalidInput
	}
	input, err := s.encryptField(record.ID, "input", record.Input)
	if err != nil {
		return EncryptedRecord{}, err
	}
	result := EncryptedRecord{ID: record.ID, RequestID: record.RequestID, GatewayKeyID: record.GatewayKeyID, PreviousResponseID: record.PreviousResponseID, IdempotencyKey: record.IdempotencyKey, RequestDigest: append([]byte(nil), record.RequestDigest...), Status: record.Status, Background: record.Background, EncryptedInput: input}
	if len(record.Request) > 0 {
		result.EncryptedRequest, err = s.encryptField(record.ID, "request", record.Request)
	}
	if len(record.Output) > 0 {
		result.EncryptedOutput, err = s.encryptField(record.ID, "output", record.Output)
	}
	if err == nil && len(record.Error) > 0 {
		result.EncryptedError, err = s.encryptField(record.ID, "error", record.Error)
	}
	return result, err
}

func (s *Service) decryptRecord(record EncryptedRecord) (Record, error) {
	input, err := s.decryptField(record.ID, "input", record.EncryptedInput)
	if err != nil {
		return Record{}, err
	}
	result := Record{ID: record.ID, RequestID: record.RequestID, GatewayKeyID: record.GatewayKeyID, PreviousResponseID: record.PreviousResponseID, IdempotencyKey: record.IdempotencyKey, RequestDigest: append([]byte(nil), record.RequestDigest...), Status: record.Status, Background: record.Background, Input: input, CancelRequestedAt: record.CancelRequestedAt, CompletedAt: record.CompletedAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if len(record.EncryptedOutput) > 0 {
		result.Output, err = s.decryptField(record.ID, "output", record.EncryptedOutput)
	}
	if err == nil && len(record.EncryptedRequest) > 0 {
		result.Request, err = s.decryptField(record.ID, "request", record.EncryptedRequest)
	}
	if err == nil && len(record.EncryptedError) > 0 {
		result.Error, err = s.decryptField(record.ID, "error", record.EncryptedError)
	}
	return result, err
}

func valueOr(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

func (s *Service) encryptField(responseID uuid.UUID, field string, value json.RawMessage) ([]byte, error) {
	if responseID == uuid.Nil || !json.Valid(value) {
		return nil, ErrInvalidInput
	}
	return s.envelope.Encrypt(value, responseAAD(responseID, field))
}

func (s *Service) decryptField(responseID uuid.UUID, field string, value []byte) (json.RawMessage, error) {
	plaintext, err := s.envelope.Decrypt(value, responseAAD(responseID, field))
	if err != nil {
		return nil, fmt.Errorf("decrypt response %s: %w", field, err)
	}
	if !json.Valid(plaintext) {
		return nil, fmt.Errorf("%w: decrypted response %s is not JSON", ErrConflict, field)
	}
	return append(json.RawMessage(nil), plaintext...), nil
}

func responseAAD(responseID uuid.UUID, field string) []byte {
	return []byte("response-record:" + responseID.String() + ":" + field)
}
