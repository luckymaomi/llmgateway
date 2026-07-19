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
	if candidate.CooldownUntil.After(requirements.At) {
		reasons = append(reasons, Exclusion{Reason: ExcludeCredentialCooling, AvailableAt: candidate.CooldownUntil})
	}
	if candidate.Concurrency.Known && candidate.Concurrency.InUse >= candidate.Concurrency.Limit {
		reasons = append(reasons, Exclusion{Reason: ExcludeConcurrencyExhausted})
	}
	if candidate.RequestsPerMinute.Known && candidate.RequestsPerMinute.Used >= candidate.RequestsPerMinute.Limit {
		reasons = append(reasons, Exclusion{Reason: ExcludeRequestsPerMinute})
	}
	if candidate.TokensPerMinute.Known &&
		candidate.TokensPerMinute.Limit-candidate.TokensPerMinute.Used < requirements.EstimatedTokens {
		reasons = append(reasons, Exclusion{Reason: ExcludeTokensPerMinute})
	}
	if !candidate.ExitHealthy {
		reasons = append(reasons, Exclusion{Reason: ExcludeExitUnhealthy})
	}
	if candidate.Quota.Source != SourceUnknown && candidate.Quota.RemainingTokens < requirements.EstimatedTokens {
		reasons = append(reasons, Exclusion{Reason: ExcludeQuotaExhausted})
	}
	if _, found := excluded[candidate.ID]; found {
		reasons = append(reasons, Exclusion{Reason: ExcludeAttempt})
	}

	available := make(map[Capability]struct{}, len(candidate.Capabilities))
	for _, capability := range candidate.Capabilities {
		available[capability] = struct{}{}
	}
	for _, capability := range requirements.Capabilities {
		if _, found := available[capability]; !found {
			reasons = append(reasons, Exclusion{Reason: ExcludeMissingCapability, Capability: capability})
		}
	}
	return reasons
}

func validateRequirements(requirements Requirements) error {
	if requirements.ModelID == "" || !validResourceDomain(requirements.ResourceDomain) || requirements.At.IsZero() {
		return newError(ErrorInvalidInput, "model, resource domain, and decision time are required", "")
	}
	if requirements.EstimatedTokens <= 0 {
		return newError(ErrorInvalidInput, "estimated tokens must be positive", "")
	}
	capabilities := make(map[Capability]struct{}, len(requirements.Capabilities))
	for _, capability := range requirements.Capabilities {
		if capability == "" {
			return newError(ErrorInvalidInput, "required capability cannot be empty", "")
		}
		if _, exists := capabilities[capability]; exists {
			return newError(ErrorInvalidInput, "required capabilities must be unique", "")
		}
		capabilities[capability] = struct{}{}
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
	if !validPermille(candidate.LoadPermille) || !validPermille(candidate.SuccessPermille) || !validPermille(candidate.ErrorPermille) {
		return newError(ErrorInvalidCandidate, "load and reliability values must be permille values", candidate.ID)
	}
	if candidate.TTFT < 0 || candidate.Latency < 0 {
		return newError(ErrorInvalidCandidate, "latency durations cannot be negative", candidate.ID)
	}
	if err := validateConcurrentCapacity(candidate.Concurrency); err != nil {
		return newError(ErrorInvalidCandidate, err.Error(), candidate.ID)
	}
	if err := validateRateCapacity(candidate.RequestsPerMinute); err != nil {
		return newError(ErrorInvalidCandidate, err.Error(), candidate.ID)
	}
	if err := validateRateCapacity(candidate.TokensPerMinute); err != nil {
		return newError(ErrorInvalidCandidate, err.Error(), candidate.ID)
	}
	if candidate.Quota.Source != SourceAuthoritative && candidate.Quota.Source != SourceEstimated && candidate.Quota.Source != SourceUnknown {
		return newError(ErrorInvalidCandidate, "quota source is invalid", candidate.ID)
	}
	if candidate.Quota.RemainingTokens < 0 || (candidate.Quota.Source == SourceUnknown && candidate.Quota.RemainingTokens != 0) {
		return newError(ErrorInvalidCandidate, "quota remaining tokens do not match their source", candidate.ID)
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

func validateConcurrentCapacity(capacity ConcurrentCapacity) error {
	if capacity.InUse < 0 {
		return newError(ErrorInvalidCandidate, "concurrency in use cannot be negative", "")
	}
	if capacity.Known && capacity.Limit <= 0 {
		return newError(ErrorInvalidCandidate, "known concurrency limit must be positive", "")
	}
	if !capacity.Known && (capacity.Limit != 0 || capacity.InUse != 0) {
		return newError(ErrorInvalidCandidate, "unknown concurrency capacity cannot carry counters", "")
	}
	return nil
}

func validateRateCapacity(capacity RateCapacity) error {
	if capacity.Used < 0 {
		return newError(ErrorInvalidCandidate, "rate usage cannot be negative", "")
	}
	if capacity.Known && capacity.Limit <= 0 {
		return newError(ErrorInvalidCandidate, "known rate limit must be positive", "")
	}
	if !capacity.Known && (capacity.Limit != 0 || capacity.Used != 0) {
		return newError(ErrorInvalidCandidate, "unknown rate capacity cannot carry counters", "")
	}
	return nil
}

func validPermille(value int32) bool {
	return value >= 0 && value <= 1000
}

func validResourceDomain(domain ResourceDomain) bool {
	return domain == ResourceFree || domain == ResourceProfessional
}
