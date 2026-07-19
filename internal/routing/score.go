package routing

import (
	"math"
	"math/bits"
	"time"
)

const maxWeight int64 = 1_000_000

func validatePolicy(policy Policy) error {
	weights := []int64{
		policy.Weights.Priority, policy.Weights.Quota, policy.Weights.Load,
		policy.Weights.Reliability, policy.Weights.TTFT, policy.Weights.Latency,
	}
	var total int64
	for _, weight := range weights {
		if weight < 0 || weight > maxWeight {
			return newError(ErrorInvalidPolicy, "score weights must be between zero and one million", "")
		}
		total += weight
	}
	if total == 0 {
		return newError(ErrorInvalidPolicy, "at least one score weight is required", "")
	}
	if policy.TTFTCeiling <= 0 || policy.LatencyCeiling <= 0 ||
		policy.TTFTCeiling > time.Duration(math.MaxInt64/1000) || policy.LatencyCeiling > time.Duration(math.MaxInt64/1000) {
		return newError(ErrorInvalidPolicy, "latency ceilings must be positive and within the fixed-point range", "")
	}
	if policy.ExplorationPermille < 0 || policy.ExplorationPermille > 1000 {
		return newError(ErrorInvalidPolicy, "exploration must be a permille value", "")
	}
	return nil
}

func scoreCandidate(policy Policy, requirements Requirements, candidate Candidate) Score {
	priority := int64(1000-candidate.AdminPriority) * policy.Weights.Priority
	quota := quotaPermille(requirements.EstimatedTokens, candidate.Quota) * policy.Weights.Quota
	load := int64(1000-candidate.LoadPermille) * policy.Weights.Load
	reliability := int64(candidate.SuccessPermille-candidate.ErrorPermille) * policy.Weights.Reliability
	ttft := durationPermille(candidate.TTFT, policy.TTFTCeiling) * policy.Weights.TTFT
	latency := durationPermille(candidate.Latency, policy.LatencyCeiling) * policy.Weights.Latency
	return Score{
		Priority: priority, Quota: quota, Load: load, Reliability: reliability,
		TTFT: ttft, Latency: latency,
		Total: priority + quota + load + reliability + ttft + latency,
	}
}

func quotaPermille(estimatedTokens int64, quota Quota) int64 {
	if quota.Source == SourceUnknown {
		return 0
	}
	if estimatedTokens == 0 {
		if quota.RemainingTokens == 0 {
			return 0
		}
		return quotaConfidence(1000, quota.Source)
	}
	if quota.RemainingTokens >= estimatedTokens {
		return quotaConfidence(1000, quota.Source)
	}
	high, low := bits.Mul64(uint64(quota.RemainingTokens), 1000)
	ratio, _ := bits.Div64(high, low, uint64(estimatedTokens))
	return quotaConfidence(int64(ratio), quota.Source)
}

func quotaConfidence(value int64, source FactSource) int64 {
	if source == SourceEstimated {
		return value * 3 / 4
	}
	return value
}

func durationPermille(observed, ceiling time.Duration) int64 {
	if observed >= ceiling {
		return 0
	}
	return int64((ceiling - observed) * 1000 / ceiling)
}
