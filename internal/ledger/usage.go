package ledger

import "math"

func DecideUsage(usage Usage) (UsageDecision, error) {
	if usage.Source != UsageAuthoritative && usage.Source != UsageEstimated && usage.Source != UsageUnknown {
		return UsageDecision{}, newError(ErrorInvalidInput, "usage source is invalid", "", "")
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 {
		return UsageDecision{}, newError(ErrorInvalidInput, "usage token counts cannot be negative", "", "")
	}
	if usage.Source == UsageUnknown {
		if usage.InputTokens != 0 || usage.OutputTokens != 0 {
			return UsageDecision{}, newError(ErrorInvalidInput, "unknown usage cannot carry token counts", "", "")
		}
		return UsageDecision{Disposition: UsageHold, Source: UsageUnknown}, nil
	}
	total, ok := addTokens(usage.InputTokens, usage.OutputTokens)
	if !ok {
		return UsageDecision{}, newError(ErrorArithmeticOverflow, "usage token total overflowed", "", "")
	}
	return UsageDecision{Disposition: UsageCharge, ChargeTokens: total, Source: usage.Source}, nil
}

func addTokens(left, right Tokens) (Tokens, bool) {
	if right > 0 && left > Tokens(math.MaxInt64)-right {
		return 0, false
	}
	if right < 0 && left < Tokens(math.MinInt64)-right {
		return 0, false
	}
	return left + right, true
}
