package publicapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/protocol"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	responseowner "github.com/luckymaomi/llmgateway/internal/responses"
)

type ResponseWorkerConfig struct {
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	StaleAfter        time.Duration
	RecoveryBatchSize int32
	MaxWorkers        int
}

func (a *API) RunResponseWorker(ctx context.Context, config ResponseWorkerConfig) {
	if a.responses == nil || config.PollInterval <= 0 || config.HeartbeatInterval <= 0 || config.StaleAfter <= 2*config.HeartbeatInterval || config.RecoveryBatchSize < 1 || config.MaxWorkers < 1 {
		a.logger.Error("background response worker configuration is invalid")
		return
	}
	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()
	workers := make(chan struct{}, config.MaxWorkers)

	for {
		if recovered, err := a.responses.RecoverOnce(ctx, config.RecoveryBatchSize); err != nil && ctx.Err() == nil {
			a.logger.Error("background response recovery failed", "error", err)
		} else if recovered > 0 {
			a.logger.Info("background responses recovered", "count", recovered)
		}
		for len(workers) < cap(workers) {
			claim, record, err := a.responses.ClaimNext(ctx, uuid.New(), time.Now().UTC().Add(-config.StaleAfter))
			if errors.Is(err, responseowner.ErrNotFound) || ctx.Err() != nil {
				break
			}
			if err != nil {
				a.logger.Error("claim background response failed", "error", err)
				break
			}
			workers <- struct{}{}
			go func() {
				defer func() {
					<-workers
					a.wakeResponseWorker()
				}()
				a.executeBackgroundResponse(ctx, config.HeartbeatInterval, claim, record)
			}()
		}

		select {
		case <-ctx.Done():
			return
		case <-a.responseWake:
		case <-ticker.C:
		}
	}
}

func (a *API) executeBackgroundResponse(parent context.Context, heartbeatInterval time.Duration, claim responseowner.Claim, record responseowner.Record) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	a.running.Store(record.ID, cancel)
	defer a.running.Delete(record.ID)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := a.responses.Heartbeat(ctx, claim); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	principal, err := a.identity.GatewayPrincipalByID(ctx, record.GatewayKeyID)
	if err != nil {
		a.terminateBackgroundResponse(claim, nil, responseowner.StatusFailed, "gateway_key_inactive", "the gateway key or its owner is no longer active")
		return
	}
	request, parseError := protocol.ParseResponsesRequest(bytes.NewReader(record.Request), record.ID.String())
	if parseError != nil || !request.Background || request.Chat.Stream {
		a.terminateBackgroundResponse(claim, nil, responseowner.StatusFailed, "stored_request_invalid", "the stored background request is invalid")
		return
	}
	_, previousMessages, previousError := a.previousResponseMessages(ctx, request.PreviousResponseID, principal.KeyID)
	if previousError != nil {
		a.terminateBackgroundResponse(claim, nil, responseowner.StatusFailed, previousError.Code, previousError.Message)
		return
	}
	if len(previousMessages) > 0 {
		request.Chat.Messages = append(previousMessages, request.Chat.Messages...)
	}

	idempotencyKey := "response:" + record.ID.String()
	digest := sha256.Sum256(record.Request)
	var acceptedRequestID *uuid.UUID
	command := requestflow.ChatCommand{
		Principal: principal, Request: request.Chat, RequestID: record.ID,
		RequestDigest: digest[:], IdempotencyKey: &idempotencyKey,
		AcceptedSink: func(persistContext context.Context, requestID uuid.UUID) error {
			if err := a.responses.LinkRequest(persistContext, claim, requestID); err != nil {
				return err
			}
			acceptedRequestID = &requestID
			return nil
		},
		ResultSink: func(persistContext context.Context, result requestflow.ChatResult) error {
			presented := protocol.PresentResponseWithID(protocol.ResponseIdentifierForRequest(record.ID.String()), result.Response, request)
			encoded, err := json.Marshal(presented)
			if err != nil {
				return err
			}
			return a.responses.StageOutput(persistContext, claim, result.RequestID, encoded)
		},
	}
	result, workflowError := a.workflow.Chat(ctx, command)
	executionCanceled := ctx.Err() != nil
	cancel()
	<-heartbeatDone
	if workflowError != nil {
		status := responseowner.StatusFailed
		if workflowError.Kind == canonical.ErrorUncertain {
			status = responseowner.StatusUncertain
		} else if workflowError.Code == "request_canceled" || executionCanceled {
			status = responseowner.StatusCanceled
		}
		a.terminateBackgroundResponse(claim, acceptedRequestID, status, workflowError.Code, workflowError.Message)
		return
	}
	terminalContext, terminalCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer terminalCancel()
	if err := a.responses.CompleteClaim(terminalContext, claim, result.RequestID); err != nil && !errors.Is(err, responseowner.ErrFenced) {
		a.logger.Error("complete background response failed", "response_id", record.ID, "request_id", result.RequestID, "error", err)
	}
}

func (a *API) terminateBackgroundResponse(claim responseowner.Claim, requestID *uuid.UUID, status responseowner.Status, code, message string) {
	body, _ := json.Marshal(map[string]any{"code": code, "message": message})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.responses.TerminateClaim(ctx, claim, requestID, status, body); err != nil && !errors.Is(err, responseowner.ErrFenced) {
		a.logger.Error("terminate background response failed", "response_id", claim.ResponseID, "status", status, "error", err)
	}
}

func (a *API) wakeResponseWorker() {
	select {
	case a.responseWake <- struct{}{}:
	default:
	}
}
