package routing

import (
	"testing"
	"time"
)

var routingTestTime = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

type sequenceRandom struct {
	values []int
	index  int
}

func (r *sequenceRandom) Intn(limit int) int {
	value := r.values[r.index]
	r.index++
	return value
}

func testPolicy(exploration int32) Policy {
	return Policy{
		Weights: Weights{
			Priority: 10, Quota: 2, Load: 4, Reliability: 3, TTFT: 1, Latency: 1,
		},
		TTFTCeiling: time.Second, LatencyCeiling: 10 * time.Second,
		ExplorationPermille: exploration,
	}
}

func testCandidate(id CandidateID) Candidate {
	return Candidate{
		ID: id, ModelID: "chat", ResourceDomain: ResourceFree,
		ModelPublished: true, CredentialAuthorized: true, CredentialActive: true,
		Capabilities: []Capability{"stream", "tools"}, ExitHealthy: true,
		Quota: Quota{Source: SourceUnknown}, AdminPriority: 100,
		LoadPermille: 200, SuccessPermille: 900, ErrorPermille: 50,
		TTFT: 200 * time.Millisecond, Latency: 2 * time.Second,
	}
}

func testRequirements() Requirements {
	return Requirements{
		ModelID: "chat", ResourceDomain: ResourceFree, Capabilities: []Capability{"tools"},
		EstimatedTokens: 100, At: routingTestTime,
	}
}

func newTestRouter(t *testing.T, exploration int32, random Random) *Router {
	t.Helper()
	router, err := NewRouter(testPolicy(exploration), random)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func evaluationFor(t *testing.T, decision Decision, candidateID CandidateID) Evaluation {
	t.Helper()
	for _, evaluation := range decision.Evaluations {
		if evaluation.CandidateID == candidateID {
			return evaluation
		}
	}
	t.Fatalf("evaluation for %q was not recorded", candidateID)
	return Evaluation{}
}

func hasReason(exclusions []Exclusion, reason ExclusionReason) bool {
	for _, exclusion := range exclusions {
		if exclusion.Reason == reason {
			return true
		}
	}
	return false
}

func TestRouterSelectsQualifiedCandidateAndExplainsEveryHardExclusion(t *testing.T) {
	router := newTestRouter(t, 0, nil)
	qualified := testCandidate("qualified")
	blocked := testCandidate("blocked")
	blocked.ResourceDomain = ResourceProfessional
	blocked.CredentialActive = false
	blocked.CooldownUntil = routingTestTime.Add(time.Minute)
	blocked.Concurrency = ConcurrentCapacity{Known: true, Limit: 2, InUse: 2}
	blocked.Capabilities = []Capability{"stream"}

	decision, err := router.Select(testRequirements(), []Candidate{blocked, qualified})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if decision.SelectedCandidateID != qualified.ID || decision.Mode != SelectionScored {
		t.Fatalf("decision = %#v", decision)
	}
	evaluation := evaluationFor(t, decision, blocked.ID)
	wantReasons := []ExclusionReason{
		ExcludeResourceDomainMismatch, ExcludeCredentialInactive, ExcludeCredentialCooling,
		ExcludeConcurrencyExhausted, ExcludeMissingCapability,
	}
	for _, reason := range wantReasons {
		if !hasReason(evaluation.Exclusions, reason) {
			t.Fatalf("exclusions = %#v, want reason %q", evaluation.Exclusions, reason)
		}
	}
}

func TestRouterUsesStableCandidateIDForEqualScores(t *testing.T) {
	router := newTestRouter(t, 0, nil)
	a := testCandidate("a")
	b := testCandidate("b")

	decision, err := router.Select(testRequirements(), []Candidate{b, a})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if decision.SelectedCandidateID != "a" {
		t.Fatalf("selected = %q, want a", decision.SelectedCandidateID)
	}
	if len(decision.Ranked) != 2 || decision.Ranked[0].CandidateID != "a" || decision.Ranked[1].CandidateID != "b" {
		t.Fatalf("ranked = %#v", decision.Ranked)
	}
}

func TestRouterHonorsEligibleAffinityAheadOfScore(t *testing.T) {
	router := newTestRouter(t, 0, nil)
	best := testCandidate("best")
	best.AdminPriority = 1
	affinity := testCandidate("affinity")
	affinity.AdminPriority = 900
	requirements := testRequirements()
	requirements.AffinityCandidateID = affinity.ID

	decision, err := router.Select(requirements, []Candidate{best, affinity})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if decision.SelectedCandidateID != affinity.ID || decision.Mode != SelectionAffinity {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.Affinity == nil || !decision.Affinity.Honored {
		t.Fatalf("affinity = %#v", decision.Affinity)
	}
}

func TestRouterEscapesIneligibleAffinityWithItsReasons(t *testing.T) {
	router := newTestRouter(t, 0, nil)
	best := testCandidate("best")
	affinity := testCandidate("affinity")
	affinity.CooldownUntil = routingTestTime.Add(time.Minute)
	requirements := testRequirements()
	requirements.AffinityCandidateID = affinity.ID

	decision, err := router.Select(requirements, []Candidate{best, affinity})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if decision.SelectedCandidateID != best.ID || decision.Mode != SelectionScored {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.Affinity == nil || decision.Affinity.Honored || !hasReason(decision.Affinity.Escape, ExcludeCredentialCooling) {
		t.Fatalf("affinity = %#v", decision.Affinity)
	}
}

func TestRouterExplorationSelectsAQualifiedAlternative(t *testing.T) {
	random := &sequenceRandom{values: []int{0, 1}}
	router := newTestRouter(t, 1000, random)
	first := testCandidate("first")
	first.AdminPriority = 1
	second := testCandidate("second")
	second.AdminPriority = 200
	third := testCandidate("third")
	third.AdminPriority = 300

	decision, err := router.Select(testRequirements(), []Candidate{third, first, second})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if decision.Mode != SelectionExploration || decision.SelectedCandidateID != third.ID {
		t.Fatalf("decision = %#v", decision)
	}
	if decision.Ranked[0].CandidateID != first.ID {
		t.Fatalf("exploration changed stable ranking: %#v", decision.Ranked)
	}
}
