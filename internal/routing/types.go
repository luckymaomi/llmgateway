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

type Candidate struct {
	ID                   CandidateID
	ModelID              ModelID
	ResourceDomain       ResourceDomain
	ModelPublished       bool
	CredentialAuthorized bool
	CredentialActive     bool
	Capabilities         []Capability
	CooldownUntil        time.Time
	AdminPriority        int32
	Weight               int32
}

type Requirements struct {
	ModelID            ModelID
	ResourceDomain     ResourceDomain
	Capabilities       []Capability
	ExcludedCandidates []CandidateID
	At                 time.Time
}

type Random interface {
	Intn(limit int) int
}

type ExclusionReason string

const (
	ExcludeModelNotPublished      ExclusionReason = "model_not_published"
	ExcludeModelMismatch          ExclusionReason = "model_mismatch"
	ExcludeResourceDomainMismatch ExclusionReason = "resource_domain_mismatch"
	ExcludeMissingCapability      ExclusionReason = "missing_capability"
	ExcludeCredentialUnauthorized ExclusionReason = "credential_unauthorized"
	ExcludeCredentialInactive     ExclusionReason = "credential_inactive"
	ExcludeCredentialCooling      ExclusionReason = "credential_cooling"
	ExcludeAttempt                ExclusionReason = "excluded_for_attempt"
)

type Exclusion struct {
	Reason      ExclusionReason
	Capability  Capability
	AvailableAt time.Time
}

type Evaluation struct {
	CandidateID CandidateID
	Eligible    bool
	Exclusions  []Exclusion
}

type RankedCandidate struct {
	CandidateID CandidateID
	Priority    int32
	Weight      int32
}

type SelectionMode string

const (
	SelectionNone             SelectionMode = "none"
	SelectionPriorityWeighted SelectionMode = "priority_weighted"
)

type Decision struct {
	SelectedCandidateID CandidateID
	Mode                SelectionMode
	Ranked              []RankedCandidate
	Evaluations         []Evaluation
}

type ErrorKind string

const (
	ErrorInvalidInput     ErrorKind = "invalid_input"
	ErrorInvalidCandidate ErrorKind = "invalid_candidate"
	ErrorDuplicate        ErrorKind = "duplicate_candidate"
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
