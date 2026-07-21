package routing

import (
	"testing"
	"time"
)

var routingTestTime = time.Date(2026, time.July, 21, 8, 0, 0, 0, time.UTC)

type fixedRandom int

func (r fixedRandom) Intn(limit int) int { return int(r) % limit }

func testCandidate(id CandidateID) Candidate {
	return Candidate{
		ID: id, ModelID: "chat", ResourceDomain: ResourceFree,
		ModelPublished: true, CredentialAuthorized: true, CredentialActive: true,
		Capabilities: []Capability{"chat", "stream", "tools"}, AdminPriority: 100, Weight: 1,
	}
}

func testRequirements() Requirements {
	return Requirements{ModelID: "chat", ResourceDomain: ResourceFree, Capabilities: []Capability{"chat", "tools"}, At: routingTestTime}
}

func TestRouterFiltersHardEligibilityBeforePriorityAndWeight(t *testing.T) {
	router, err := NewRouter(fixedRandom(0))
	if err != nil {
		t.Fatal(err)
	}
	blocked := testCandidate("blocked")
	blocked.AdminPriority = 1
	blocked.CredentialActive = false
	blocked.CooldownUntil = routingTestTime.Add(time.Minute)
	blocked.Capabilities = []Capability{"chat"}
	qualified := testCandidate("qualified")
	qualified.AdminPriority = 500

	decision, err := router.Select(testRequirements(), []Candidate{blocked, qualified})
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedCandidateID != qualified.ID || decision.Mode != SelectionPriorityWeighted {
		t.Fatalf("decision = %#v", decision)
	}
	evaluation := evaluationFor(t, decision, blocked.ID)
	for _, reason := range []ExclusionReason{ExcludeCredentialInactive, ExcludeCredentialCooling, ExcludeMissingCapability} {
		if !hasReason(evaluation.Exclusions, reason) {
			t.Fatalf("exclusions = %#v, want %q", evaluation.Exclusions, reason)
		}
	}
}

func TestRouterUsesOnlyTheBestPriorityAndHonorsWeight(t *testing.T) {
	router, err := NewRouter(fixedRandom(3))
	if err != nil {
		t.Fatal(err)
	}
	first := testCandidate("first")
	first.AdminPriority, first.Weight = 10, 1
	second := testCandidate("second")
	second.AdminPriority, second.Weight = 10, 4
	reserve := testCandidate("reserve")
	reserve.AdminPriority, reserve.Weight = 20, 1000

	decision, err := router.Select(testRequirements(), []Candidate{reserve, second, first})
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedCandidateID != second.ID {
		t.Fatalf("selected = %q, want weighted candidate %q", decision.SelectedCandidateID, second.ID)
	}
	if len(decision.Ranked) != 3 || decision.Ranked[0].CandidateID != first.ID || decision.Ranked[1].CandidateID != second.ID || decision.Ranked[2].CandidateID != reserve.ID {
		t.Fatalf("ranked = %#v", decision.Ranked)
	}
}

func TestRouterExcludesPreviousAttemptAndResourceDomain(t *testing.T) {
	router, err := NewRouter(fixedRandom(0))
	if err != nil {
		t.Fatal(err)
	}
	previous := testCandidate("previous")
	professional := testCandidate("professional")
	professional.ResourceDomain = ResourceProfessional
	requirements := testRequirements()
	requirements.ExcludedCandidates = []CandidateID{previous.ID}

	decision, err := router.Select(requirements, []Candidate{previous, professional})
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedCandidateID != "" || decision.Mode != SelectionNone {
		t.Fatalf("decision = %#v", decision)
	}
	if !hasReason(evaluationFor(t, decision, previous.ID).Exclusions, ExcludeAttempt) || !hasReason(evaluationFor(t, decision, professional.ID).Exclusions, ExcludeResourceDomainMismatch) {
		t.Fatalf("evaluations = %#v", decision.Evaluations)
	}
}

func TestRouterReportsEarliestCandidateAvailableOnlyForPureCooldown(t *testing.T) {
	router, err := NewRouter(fixedRandom(0))
	if err != nil {
		t.Fatal(err)
	}
	later := testCandidate("later")
	later.CooldownUntil = routingTestTime.Add(2 * time.Minute)
	sooner := testCandidate("sooner")
	sooner.CooldownUntil = routingTestTime.Add(time.Minute)
	permanent := testCandidate("permanent")
	permanent.CredentialActive = false
	permanent.CooldownUntil = routingTestTime.Add(30 * time.Second)

	decision, err := router.Select(testRequirements(), []Candidate{later, permanent, sooner})
	if err != nil {
		t.Fatal(err)
	}
	if decision.SelectedCandidateID != "" || !decision.NextAvailableAt.Equal(sooner.CooldownUntil) {
		t.Fatalf("decision = %#v", decision)
	}
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
