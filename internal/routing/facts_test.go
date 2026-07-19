package routing

import "testing"

func TestRouterKeepsUnknownQuotaEligibleAndNeutral(t *testing.T) {
	router := newTestRouter(t, 0, nil)
	unknown := testCandidate("unknown")
	known := testCandidate("known")
	known.Quota = Quota{Source: SourceAuthoritative, RemainingTokens: 1_000}

	decision, err := router.Select(testRequirements(), []Candidate{unknown, known})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	unknownEvaluation := evaluationFor(t, decision, unknown.ID)
	knownEvaluation := evaluationFor(t, decision, known.ID)
	if !unknownEvaluation.Eligible || unknownEvaluation.Score.Quota != 0 {
		t.Fatalf("unknown quota evaluation = %#v", unknownEvaluation)
	}
	if !knownEvaluation.Eligible || knownEvaluation.Score.Quota <= 0 {
		t.Fatalf("known quota evaluation = %#v", knownEvaluation)
	}
}

func TestRouterRecordsAttemptExclusionAndStaysInResourceDomain(t *testing.T) {
	router := newTestRouter(t, 0, nil)
	previous := testCandidate("previous")
	professional := testCandidate("professional")
	professional.ResourceDomain = ResourceProfessional
	requirements := testRequirements()
	requirements.ExcludedCandidates = []CandidateID{previous.ID}

	decision, err := router.Select(requirements, []Candidate{previous, professional})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if decision.Mode != SelectionNone || decision.SelectedCandidateID != "" {
		t.Fatalf("decision = %#v", decision)
	}
	if !hasReason(evaluationFor(t, decision, previous.ID).Exclusions, ExcludeAttempt) {
		t.Fatalf("previous candidate evaluation = %#v", evaluationFor(t, decision, previous.ID))
	}
	if !hasReason(evaluationFor(t, decision, professional.ID).Exclusions, ExcludeResourceDomainMismatch) {
		t.Fatalf("professional candidate evaluation = %#v", evaluationFor(t, decision, professional.ID))
	}
}

func TestRouterReportsMissingAffinityBeforeUsingQualifiedCandidates(t *testing.T) {
	router := newTestRouter(t, 0, nil)
	requirements := testRequirements()
	requirements.AffinityCandidateID = "retired"

	decision, err := router.Select(requirements, []Candidate{testCandidate("available")})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if decision.SelectedCandidateID != "available" {
		t.Fatalf("selected = %q", decision.SelectedCandidateID)
	}
	if decision.Affinity == nil || !hasReason(decision.Affinity.Escape, ExcludeAffinityCandidateMissing) {
		t.Fatalf("affinity = %#v", decision.Affinity)
	}
}
