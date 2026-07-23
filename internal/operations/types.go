package operations

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrForbidden = errors.New("operations overview forbidden")

type RequestSummary struct {
	RequestCount      int64
	CompletedCount    int64
	FailedCount       int64
	UncertainCount    int64
	InputTokens       int64
	OutputTokens      int64
	FirstByteP95Ms    int64
	TotalLatencyP95Ms int64
}

type TrendPoint struct {
	Bucket       time.Time
	RequestCount int64
	InputTokens  int64
	OutputTokens int64
}

type ErrorCount struct {
	Kind  string
	Count int64
}

type AdministratorResources struct {
	ResourcePoolCount              int64
	ActiveResourcePoolCount        int64
	ConnectedProviderCount         int64
	ModelCount                     int64
	CredentialCount                int64
	ActiveCredentialCount          int64
	CoolingCredentialCount         int64
	SuccessfulCredentialProbeCount int64
	ActiveMemberCount              int64
	ActiveGatewayKeyCount          int64
	ActiveServicePlanCount         int64
	ActiveSubscriptionCount        int64
	HasActiveUpstream              bool
	HasModelPrice                  bool
	HasCompletedRequest            bool
}

type MemberAccess struct {
	ActiveGatewayKeyCount     int64
	ActiveSubscriptionCount   int64
	RemainingTokens           int64
	NearestSubscriptionExpiry *time.Time
}

type AdministratorOverview struct {
	Resources AdministratorResources
	Requests  RequestSummary
	Trend     []TrendPoint
	Errors    []ErrorCount
}

type MemberOverview struct {
	Access   MemberAccess
	Requests RequestSummary
	Trend    []TrendPoint
	Errors   []ErrorCount
}

type Overview struct {
	Administrator *AdministratorOverview
	Member        *MemberOverview
}

type Repository interface {
	AdministratorResources(context.Context, time.Time) (AdministratorResources, error)
	MemberAccess(context.Context, uuid.UUID, time.Time) (MemberAccess, error)
	RequestSummary(context.Context, *uuid.UUID, time.Time, time.Time) (RequestSummary, error)
	RequestTrend(context.Context, *uuid.UUID, time.Time, time.Time) ([]TrendPoint, error)
	RequestErrors(context.Context, *uuid.UUID, time.Time, time.Time) ([]ErrorCount, error)
}
