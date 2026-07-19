package routing

import "sort"

type Router struct {
	policy Policy
	random Random
}

func NewRouter(policy Policy, random Random) (*Router, error) {
	if err := validatePolicy(policy); err != nil {
		return nil, err
	}
	if policy.ExplorationPermille > 0 && random == nil {
		return nil, newError(ErrorInvalidPolicy, "exploration requires a random source", "")
	}
	return &Router{policy: policy, random: random}, nil
}

func (r *Router) Select(requirements Requirements, candidates []Candidate) (Decision, error) {
	if err := validateRequirements(requirements); err != nil {
		return Decision{}, err
	}
	excluded := make(map[CandidateID]struct{}, len(requirements.ExcludedCandidates))
	for _, candidateID := range requirements.ExcludedCandidates {
		if candidateID == "" {
			return Decision{}, newError(ErrorInvalidInput, "excluded candidate ID cannot be empty", "")
		}
		if _, exists := excluded[candidateID]; exists {
			return Decision{}, newError(ErrorInvalidInput, "excluded candidate IDs must be unique", candidateID)
		}
		excluded[candidateID] = struct{}{}
	}

	ordered := append([]Candidate(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	seen := make(map[CandidateID]struct{}, len(ordered))
	decision := Decision{Mode: SelectionNone, Evaluations: make([]Evaluation, 0, len(ordered))}
	evaluationIndex := make(map[CandidateID]int, len(ordered))
	for _, candidate := range ordered {
		if err := validateCandidate(candidate); err != nil {
			return Decision{}, err
		}
		if _, exists := seen[candidate.ID]; exists {
			return Decision{}, newError(ErrorInvalidInput, "candidate IDs must be unique", candidate.ID)
		}
		seen[candidate.ID] = struct{}{}
		reasons := evaluateEligibility(requirements, candidate, excluded)
		evaluation := Evaluation{CandidateID: candidate.ID, Eligible: len(reasons) == 0, Exclusions: reasons}
		if evaluation.Eligible {
			evaluation.Score = scoreCandidate(r.policy, requirements, candidate)
			decision.Ranked = append(decision.Ranked, RankedCandidate{CandidateID: candidate.ID, Score: evaluation.Score})
		}
		evaluationIndex[candidate.ID] = len(decision.Evaluations)
		decision.Evaluations = append(decision.Evaluations, evaluation)
	}
	sort.SliceStable(decision.Ranked, func(i, j int) bool {
		if decision.Ranked[i].Score.Total == decision.Ranked[j].Score.Total {
			return decision.Ranked[i].CandidateID < decision.Ranked[j].CandidateID
		}
		return decision.Ranked[i].Score.Total > decision.Ranked[j].Score.Total
	})

	if requirements.AffinityCandidateID != "" {
		affinity := &AffinityDecision{CandidateID: requirements.AffinityCandidateID}
		if index, found := evaluationIndex[requirements.AffinityCandidateID]; found {
			evaluation := decision.Evaluations[index]
			if evaluation.Eligible {
				affinity.Honored = true
				decision.Affinity = affinity
				decision.SelectedCandidateID = evaluation.CandidateID
				decision.SelectedScore = evaluation.Score
				decision.Mode = SelectionAffinity
				return decision, nil
			}
			affinity.Escape = append(affinity.Escape, evaluation.Exclusions...)
		} else {
			affinity.Escape = []Exclusion{{Reason: ExcludeAffinityCandidateMissing}}
		}
		decision.Affinity = affinity
	}

	if len(decision.Ranked) == 0 {
		return decision, nil
	}
	selected := 0
	mode := SelectionScored
	if len(decision.Ranked) > 1 && r.policy.ExplorationPermille > 0 {
		roll, err := r.randomIndex(1000)
		if err != nil {
			return Decision{}, err
		}
		if int32(roll) < r.policy.ExplorationPermille {
			offset, offsetErr := r.randomIndex(len(decision.Ranked) - 1)
			if offsetErr != nil {
				return Decision{}, offsetErr
			}
			selected = 1 + offset
			mode = SelectionExploration
		}
	}
	decision.SelectedCandidateID = decision.Ranked[selected].CandidateID
	decision.SelectedScore = decision.Ranked[selected].Score
	decision.Mode = mode
	return decision, nil
}

func (r *Router) randomIndex(limit int) (int, error) {
	value := r.random.Intn(limit)
	if value < 0 || value >= limit {
		return 0, newError(ErrorRandomSource, "random source returned a value outside its requested range", "")
	}
	return value, nil
}
