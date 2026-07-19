package canonical

type UsageSource string

const (
	UsageAuthoritative UsageSource = "authoritative"
	UsageEstimated     UsageSource = "estimated"
	UsageUnknown       UsageSource = "unknown"
)

// Usage keeps optional counters as pointers so an omitted upstream fact is not
// confused with an authoritative zero.
type Usage struct {
	InputTokens          *int64
	OutputTokens         *int64
	TotalTokens          *int64
	CachedInputTokens    *int64
	CacheMissInputTokens *int64
	ReasoningTokens      *int64
	Source               UsageSource
}
