package routing

import "time"

type CandidateID string
type ModelID string
type ResourceDomain string
type Capability string

const (
	ResourceFree         ResourceDomain = "free"
	ResourceProfessional ResourceDomain = "professional"
)

type FactSource string

const (
	SourceAuthoritative FactSource = "authoritative"
	SourceEstimated     FactSource = "estimated"
	SourceUnknown       FactSource = "unknown"
)

type ConcurrentCapacity struct {
	Known bool
	Limit int
	InUse int
}

type RateCapacity struct {
	Known bool
	Limit int64
	Used  int64
}

type Quota struct {
	Source          FactSource
	RemainingTokens int64
}

type Candidate struct {
	ID                   CandidateID
	ModelID              ModelID
	ResourceDomain       ResourceDomain
	ModelPublished       bool
	CredentialAuthorized bool
	CredentialActive     bool
	Capabilities         []Capability
	CooldownUntil        time.Time
	Concurrency          ConcurrentCapacity
	RequestsPerMinute    RateCapacity
	TokensPerMinute      RateCapacity
	ExitHealthy          bool
	Quota                Quota

	AdminPriority   int32
	LoadPermille    int32
	SuccessPermille int32
	ErrorPermille   int32
	TTFT            time.Duration
	Latency         time.Duration
}

type Requirements struct {
	ModelID             ModelID
	ResourceDomain      ResourceDomain
	Capabilities        []Capability
	EstimatedTokens     int64
	AffinityCandidateID CandidateID
	ExcludedCandidates  []CandidateID
	At                  time.Time
}

type Weights struct {
	Priority    int64
	Quota       int64
	Load        int64
	Reliability int64
	TTFT        int64
	Latency     int64
}

type Policy struct {
	Weights             Weights
	TTFTCeiling         time.Duration
	LatencyCeiling      time.Duration
	ExplorationPermille int32
}

type Random interface {
	Intn(limit int) int
}

type ExclusionReason string

const (
	ExcludeModelNotPublished        ExclusionReason = "model_not_published"
	ExcludeModelMismatch            ExclusionReason = "model_mismatch"
	ExcludeResourceDomainMismatch   ExclusionReason = "resource_domain_mismatch"
	ExcludeMissingCapability        ExclusionReason = "missing_capability"
	ExcludeCredentialUnauthorized   ExclusionReason = "credential_unauthorized"
	ExcludeCredentialInactive       ExclusionReason = "credential_inactive"
	ExcludeCredentialCooling        ExclusionReason = "credential_cooling"
	ExcludeConcurrencyExhausted     ExclusionReason = "concurrency_exhausted"
	ExcludeRequestsPerMinute        ExclusionReason = "requests_per_minute_exhausted"
	ExcludeTokensPerMinute          ExclusionReason = "tokens_per_minute_exhausted"
	ExcludeExitUnhealthy            ExclusionReason = "exit_unhealthy"
	ExcludeQuotaExhausted           ExclusionReason = "quota_exhausted"
	ExcludeAttempt                  ExclusionReason = "excluded_for_attempt"
	ExcludeAffinityCandidateMissing ExclusionReason = "affinity_candidate_missing"
)

type Exclusion struct {
	Reason      ExclusionReason
	Capability  Capability
	AvailableAt time.Time
}

type Score struct {
	Priority    int64
	Quota       int64
	Load        int64
	Reliability int64
	TTFT        int64
	Latency     int64
	Total       int64
}

type Evaluation struct {
	CandidateID CandidateID
	Eligible    bool
	Exclusions  []Exclusion
	Score       Score
}

type RankedCandidate struct {
	CandidateID CandidateID
	Score       Score
}

type AffinityDecision struct {
	CandidateID CandidateID
	Honored     bool
	Escape      []Exclusion
}

type SelectionMode string

const (
	SelectionNone        SelectionMode = "none"
	SelectionAffinity    SelectionMode = "affinity"
	SelectionScored      SelectionMode = "scored"
	SelectionExploration SelectionMode = "exploration"
)

type Decision struct {
	SelectedCandidateID CandidateID
	Mode                SelectionMode
	SelectedScore       Score
	Ranked              []RankedCandidate
	Evaluations         []Evaluation
	Affinity            *AffinityDecision
}

type ErrorKind string

const (
	ErrorInvalidPolicy    ErrorKind = "invalid_policy"
	ErrorInvalidInput     ErrorKind = "invalid_input"
	ErrorInvalidCandidate ErrorKind = "invalid_candidate"
	ErrorRandomSource     ErrorKind = "random_source"
)

type Error struct {
	Kind        ErrorKind
	Message     string
	CandidateID CandidateID
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func newError(kind ErrorKind, message string, candidateID CandidateID) *Error {
	return &Error{Kind: kind, Message: message, CandidateID: candidateID}
}
