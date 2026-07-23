package controlapi

import (
	"fmt"
	"strings"
	"time"

	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/quota"
)

type requestLogView struct {
	ID                string               `json:"id"`
	RequestID         string               `json:"requestId"`
	AcceptedAt        time.Time            `json:"acceptedAt"`
	CompletedAt       *time.Time           `json:"completedAt,omitempty"`
	UpdatedAt         time.Time            `json:"updatedAt"`
	UserID            string               `json:"userId"`
	UserName          string               `json:"userName"`
	GatewayKeyID      string               `json:"gatewayKeyId"`
	KeyPrefix         string               `json:"keyPrefix"`
	ModelID           string               `json:"modelId"`
	ModelAlias        string               `json:"modelAlias"`
	ResourceDomain    quota.ResourceDomain `json:"resourceDomain"`
	Status            quota.RequestStatus  `json:"status"`
	Stream            bool                 `json:"stream"`
	InputTokens       *int64               `json:"inputTokens,omitempty"`
	OutputTokens      *int64               `json:"outputTokens,omitempty"`
	UsageSource       quota.UsageSource    `json:"usageSource"`
	ErrorKind         *string              `json:"errorKind,omitempty"`
	AttemptCount      int64                `json:"attemptCount"`
	LastAttemptStatus *string              `json:"lastAttemptStatus,omitempty"`
}

type requestAttemptView struct {
	ID             string            `json:"id"`
	Sequence       int32             `json:"sequence"`
	Status         string            `json:"status"`
	ProviderName   *string           `json:"providerName,omitempty"`
	CredentialName *string           `json:"credentialName,omitempty"`
	HTTPStatus     *int32            `json:"httpStatus,omitempty"`
	ErrorKind      *string           `json:"errorKind,omitempty"`
	RetryAfterAt   *time.Time        `json:"retryAfterAt,omitempty"`
	SentAt         *time.Time        `json:"sentAt,omitempty"`
	FirstByteAt    *time.Time        `json:"firstByteAt,omitempty"`
	CompletedAt    *time.Time        `json:"completedAt,omitempty"`
	InputTokens    *int64            `json:"inputTokens,omitempty"`
	OutputTokens   *int64            `json:"outputTokens,omitempty"`
	UsageSource    quota.UsageSource `json:"usageSource"`
	CreatedAt      time.Time         `json:"createdAt"`
}

type requestLogDetailView struct {
	Request  requestLogView       `json:"request"`
	Attempts []requestAttemptView `json:"attempts"`
}

func presentRequestLogs(principal identity.Principal, items []quota.RequestLog) ([]requestLogView, error) {
	views := make([]requestLogView, 0, len(items))
	for _, item := range items {
		view, err := presentRequestLog(principal, item)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func presentRequestLogDetail(principal identity.Principal, detail quota.RequestLogDetail) (requestLogDetailView, error) {
	request, err := presentRequestLog(principal, detail.RequestLog)
	if err != nil {
		return requestLogDetailView{}, err
	}
	attempts := make([]requestAttemptView, 0, len(detail.Attempts))
	for _, attempt := range detail.Attempts {
		view := requestAttemptView{
			ID: attempt.ID.String(), Sequence: attempt.Sequence, Status: attempt.Status,
			HTTPStatus: attempt.HTTPStatus, ErrorKind: attempt.ErrorKind, RetryAfterAt: attempt.RetryAfterAt,
			SentAt: attempt.SentAt, FirstByteAt: attempt.FirstByteAt, CompletedAt: attempt.CompletedAt,
			InputTokens: attempt.InputTokens, OutputTokens: attempt.OutputTokens,
			UsageSource: attempt.UsageSource, CreatedAt: attempt.CreatedAt.UTC(),
		}
		if principal.Role == identity.RoleAdministrator {
			providerName := strings.TrimSpace(attempt.ProviderName)
			credentialName := strings.TrimSpace(attempt.CredentialName)
			if providerName == "" || credentialName == "" {
				return requestLogDetailView{}, fmt.Errorf("quota presentation: attempt %s has incomplete upstream display facts", attempt.ID)
			}
			view.ProviderName, view.CredentialName = &providerName, &credentialName
		}
		attempts = append(attempts, view)
	}
	return requestLogDetailView{Request: request, Attempts: attempts}, nil
}

func presentRequestLog(principal identity.Principal, item quota.RequestLog) (requestLogView, error) {
	if principal.Role == identity.RoleMember && item.UserID != principal.UserID {
		return requestLogView{}, fmt.Errorf("quota presentation: member request escaped the authenticated owner scope")
	}
	userName := strings.TrimSpace(item.UserName)
	keyPrefix := strings.TrimSpace(item.KeyPrefix)
	modelAlias := strings.TrimSpace(item.ModelAlias)
	if userName == "" || keyPrefix == "" || modelAlias == "" {
		return requestLogView{}, fmt.Errorf("quota presentation: request %s has incomplete display facts", item.RequestID)
	}
	return requestLogView{
		ID: item.RequestID.String(), RequestID: item.RequestID.String(), AcceptedAt: item.AcceptedAt.UTC(),
		CompletedAt: item.CompletedAt, UpdatedAt: item.UpdatedAt.UTC(), UserID: item.UserID.String(), UserName: userName,
		GatewayKeyID: item.GatewayKeyID.String(), KeyPrefix: keyPrefix, ModelID: item.ModelID.String(), ModelAlias: modelAlias,
		ResourceDomain: item.ResourceDomain, Status: item.Status, Stream: item.Stream,
		InputTokens: item.InputTokens, OutputTokens: item.OutputTokens, UsageSource: item.UsageSource,
		ErrorKind: item.ErrorKind, AttemptCount: item.AttemptCount, LastAttemptStatus: item.LastAttemptStatus,
	}, nil
}
