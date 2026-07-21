package responses

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput  = errors.New("invalid response input")
	ErrNotFound      = errors.New("response not found")
	ErrNotCancelable = errors.New("response is not cancelable")
	ErrConflict      = errors.New("response state conflict")
	ErrFenced        = errors.New("response execution fenced")
)

type Status string

const (
	StatusQueued     Status = "queued"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCanceled   Status = "canceled"
	StatusUncertain  Status = "uncertain"
)

type Record struct {
	ID                 uuid.UUID
	RequestID          *uuid.UUID
	GatewayKeyID       uuid.UUID
	PreviousResponseID *uuid.UUID
	IdempotencyKey     *string
	RequestDigest      []byte
	Status             Status
	Background         bool
	Input              json.RawMessage
	Request            json.RawMessage
	Output             json.RawMessage
	Error              json.RawMessage
	CancelRequestedAt  *time.Time
	CompletedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type EncryptedRecord struct {
	ID                   uuid.UUID
	RequestID            *uuid.UUID
	GatewayKeyID         uuid.UUID
	PreviousResponseID   *uuid.UUID
	IdempotencyKey       *string
	RequestDigest        []byte
	Status               Status
	Background           bool
	EncryptedInput       []byte
	EncryptedRequest     []byte
	EncryptedOutput      []byte
	EncryptedError       []byte
	ExecutionID          *uuid.UUID
	ExecutionGeneration  int64
	ExecutionClaimedAt   *time.Time
	ExecutionHeartbeatAt *time.Time
	CancelRequestedAt    *time.Time
	CompletedAt          *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type Claim struct {
	ResponseID  uuid.UUID
	ExecutionID uuid.UUID
	Generation  int64
}

func (claim Claim) Valid() bool {
	return claim.ResponseID != uuid.Nil && claim.ExecutionID != uuid.Nil && claim.Generation > 0
}

type Recovery struct {
	ResponseID     uuid.UUID
	ResponseStatus Status
	HasOutput      bool
	RequestStatus  string
	ErrorKind      *string
	ErrorDetail    *string
}

type Repository interface {
	Create(context.Context, EncryptedRecord) (EncryptedRecord, error)
	CreateCompleted(context.Context, EncryptedRecord) (EncryptedRecord, error)
	CreateBackground(context.Context, EncryptedRecord) (EncryptedRecord, error)
	Complete(context.Context, uuid.UUID, []byte) (EncryptedRecord, error)
	Fail(context.Context, uuid.UUID, []byte) (EncryptedRecord, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (EncryptedRecord, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
	RequestCancellation(context.Context, uuid.UUID, uuid.UUID) (EncryptedRecord, error)
	ClaimBackground(context.Context, uuid.UUID, time.Time) (EncryptedRecord, error)
	HeartbeatBackground(context.Context, Claim) error
	LinkBackgroundRequest(context.Context, Claim, uuid.UUID) error
	StageBackgroundOutput(context.Context, Claim, uuid.UUID, []byte) error
	CompleteBackground(context.Context, Claim, uuid.UUID) error
	TerminateBackground(context.Context, Claim, *uuid.UUID, Status, []byte) error
	ListBackgroundRecoveries(context.Context, int32) ([]Recovery, error)
	AttachBackgroundRequest(context.Context, uuid.UUID) error
	FinalizeRecoveredBackground(context.Context, uuid.UUID, Status, []byte) error
}

type Observer interface {
	BackgroundResponse(outcome string)
}
