package coordination

import "time"

type Scope string

const (
	ScopeGlobal     Scope = "global"
	ScopeUser       Scope = "user"
	ScopeGatewayKey Scope = "gateway_key"
	ScopeModel      Scope = "model"
	ScopeProvider   Scope = "provider"
	ScopeCredential Scope = "credential"
)

// Dimension identifies a coordination scope. SubjectID should be an internal,
// stable opaque ID. It is HMAC-derived before it enters a Valkey key.
type Dimension struct {
	Scope     Scope
	SubjectID string
}

func GlobalDimension() Dimension {
	return Dimension{Scope: ScopeGlobal}
}

type BucketMetric string

const (
	MetricRequests BucketMetric = "requests"
	MetricTokens   BucketMetric = "tokens"
)

// BucketLimit describes one continuously refilling token bucket.
type BucketLimit struct {
	Dimension       Dimension
	Metric          BucketMetric
	CapacityTokens  int64
	RefillTokens    int64
	RefillInterval  time.Duration
	RequestedTokens int64
}

type BucketState struct {
	Dimension       Dimension
	Metric          BucketMetric
	RemainingTokens int64
}

type RateDecision struct {
	Granted    bool
	ObservedAt time.Time
	RetryAt    time.Time
	Buckets    []BucketState
}

type ConcurrencyLimit struct {
	Dimension   Dimension
	MaxInFlight int64
}

// LeaseRef is safe to reconstruct on another gateway instance from durable
// request facts. ID and Dimensions must remain unchanged for the lease lifetime.
type LeaseRef struct {
	ID         string
	Dimensions []Dimension
}

type ConcurrencyState struct {
	Dimension Dimension
	InUse     int64
	Limit     int64
}

type LeaseDecision struct {
	Granted    bool
	Lease      LeaseRef
	ObservedAt time.Time
	ExpiresAt  time.Time
	RetryAt    time.Time
	Dimensions []ConcurrencyState
}

type CleanupResult struct {
	ObservedAt time.Time
	Removed    int64
	InUse      []ConcurrencyState
}
