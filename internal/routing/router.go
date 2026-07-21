package routing

import "sort"

type Router struct {
	random Random
}

func NewRouter(random Random) (*Router, error) {
	if random == nil {
		return nil, newError(ErrorRandomSource, "weighted routing requires a random source", "")
	}
	return &Router{random: random}, nil
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
		excluded[candidateID] = struct{}{}
	}

	decision := Decision{Mode: SelectionNone, Evaluations: make([]Evaluation, 0, len(candidates))}
	seen := make(map[CandidateID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := validateCandidate(candidate); err != nil {
			return Decision{}, err
		}
		if _, exists := seen[candidate.ID]; exists {
			return Decision{}, newError(ErrorDuplicate, "candidate IDs must be unique", candidate.ID)
		}
		seen[candidate.ID] = struct{}{}
		reasons := evaluateEligibility(requirements, candidate, excluded)
		decision.Evaluations = append(decision.Evaluations, Evaluation{CandidateID: candidate.ID, Eligible: len(reasons) == 0, Exclusions: reasons})
		if len(reasons) == 1 && reasons[0].Reason == ExcludeCredentialCooling &&
			(decision.NextAvailableAt.IsZero() || reasons[0].AvailableAt.Before(decision.NextAvailableAt)) {
			decision.NextAvailableAt = reasons[0].AvailableAt
		}
		if len(reasons) == 0 {
			decision.Ranked = append(decision.Ranked, RankedCandidate{CandidateID: candidate.ID, Priority: candidate.AdminPriority, Weight: candidate.Weight})
		}
	}
	if len(decision.Ranked) == 0 {
		return decision, nil
	}
	sort.Slice(decision.Ranked, func(i, j int) bool {
		if decision.Ranked[i].Priority == decision.Ranked[j].Priority {
			return decision.Ranked[i].CandidateID < decision.Ranked[j].CandidateID
		}
		return decision.Ranked[i].Priority < decision.Ranked[j].Priority
	})

	bestPriority := decision.Ranked[0].Priority
	totalWeight := 0
	eligibleCount := 0
	for _, candidate := range decision.Ranked {
		if candidate.Priority != bestPriority {
			break
		}
		totalWeight += int(candidate.Weight)
		eligibleCount++
	}
	selected := 0
	if eligibleCount > 1 {
		roll := r.random.Intn(totalWeight)
		if roll < 0 || roll >= totalWeight {
			return Decision{}, newError(ErrorRandomSource, "random source returned a value outside its requested range", "")
		}
		for index := 0; index < eligibleCount; index++ {
			roll -= int(decision.Ranked[index].Weight)
			if roll < 0 {
				selected = index
				break
			}
		}
	}
	decision.SelectedCandidateID = decision.Ranked[selected].CandidateID
	decision.Mode = SelectionPriorityWeighted
	return decision, nil
}
