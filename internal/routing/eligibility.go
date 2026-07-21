package routing

func evaluateEligibility(requirements Requirements, candidate Candidate, excluded map[CandidateID]struct{}) []Exclusion {
	var reasons []Exclusion
	if !candidate.ModelPublished {
		reasons = append(reasons, Exclusion{Reason: ExcludeModelNotPublished})
	}
	if candidate.ModelID != requirements.ModelID {
		reasons = append(reasons, Exclusion{Reason: ExcludeModelMismatch})
	}
	if candidate.ResourceDomain != requirements.ResourceDomain {
		reasons = append(reasons, Exclusion{Reason: ExcludeResourceDomainMismatch})
	}
	if !candidate.CredentialAuthorized {
		reasons = append(reasons, Exclusion{Reason: ExcludeCredentialUnauthorized})
	}
	if !candidate.CredentialActive {
		reasons = append(reasons, Exclusion{Reason: ExcludeCredentialInactive})
	}
	if !candidate.CooldownUntil.IsZero() && requirements.At.Before(candidate.CooldownUntil) {
		reasons = append(reasons, Exclusion{Reason: ExcludeCredentialCooling, AvailableAt: candidate.CooldownUntil})
	}
	if _, found := excluded[candidate.ID]; found {
		reasons = append(reasons, Exclusion{Reason: ExcludeAttempt})
	}
	capabilities := make(map[Capability]struct{}, len(candidate.Capabilities))
	for _, capability := range candidate.Capabilities {
		capabilities[capability] = struct{}{}
	}
	for _, capability := range requirements.Capabilities {
		if _, found := capabilities[capability]; !found {
			reasons = append(reasons, Exclusion{Reason: ExcludeMissingCapability, Capability: capability})
		}
	}
	return reasons
}

func validateRequirements(requirements Requirements) error {
	if requirements.ModelID == "" || !validResourceDomain(requirements.ResourceDomain) || requirements.At.IsZero() {
		return newError(ErrorInvalidInput, "model, resource domain, and decision time are required", "")
	}
	seen := make(map[Capability]struct{}, len(requirements.Capabilities))
	for _, capability := range requirements.Capabilities {
		if capability == "" {
			return newError(ErrorInvalidInput, "required capability cannot be empty", "")
		}
		if _, exists := seen[capability]; exists {
			return newError(ErrorInvalidInput, "required capabilities must be unique", "")
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func validateCandidate(candidate Candidate) error {
	if candidate.ID == "" || candidate.ModelID == "" || !validResourceDomain(candidate.ResourceDomain) {
		return newError(ErrorInvalidCandidate, "candidate ID, model, and resource domain are required", candidate.ID)
	}
	if candidate.AdminPriority < 0 || candidate.AdminPriority > 1000 {
		return newError(ErrorInvalidCandidate, "admin priority must be between 0 and 1000", candidate.ID)
	}
	if candidate.Weight <= 0 || candidate.Weight > 1000 {
		return newError(ErrorInvalidCandidate, "weight must be between 1 and 1000", candidate.ID)
	}
	seen := make(map[Capability]struct{}, len(candidate.Capabilities))
	for _, capability := range candidate.Capabilities {
		if capability == "" {
			return newError(ErrorInvalidCandidate, "candidate capability cannot be empty", candidate.ID)
		}
		if _, exists := seen[capability]; exists {
			return newError(ErrorInvalidCandidate, "candidate capabilities must be unique", candidate.ID)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func validResourceDomain(domain ResourceDomain) bool {
	return domain == ResourceFree || domain == ResourceProfessional
}
