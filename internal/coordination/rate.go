package coordination

import (
	"context"
	"fmt"
	"math"
	"time"
)

const (
	maximumBucketTokens   = int64(1_000_000_000_000)
	maximumRefillInterval = 24 * time.Hour
	maximumFullRefill     = 30 * 24 * time.Hour
	maximumRateDimensions = 16
)

// AcquireRate atomically deducts every supplied bucket or none of them. A
// Valkey or script failure returns ErrUnavailable and never grants capacity.
func (c *Coordinator) AcquireRate(ctx context.Context, limits []BucketLimit) (RateDecision, error) {
	keys, arguments, err := c.prepareRateLimits(limits)
	if err != nil {
		return RateDecision{}, err
	}
	values, err := acquireRateScript.Run(ctx, c.client, keys, arguments...).Int64Slice()
	if err != nil {
		return RateDecision{}, unavailable("acquire rate tokens", err)
	}
	if len(values) != 3+len(limits) || (values[0] != 0 && values[0] != 1) || values[1] <= 0 || values[2] < 0 {
		return RateDecision{}, unavailable("acquire rate tokens", fmt.Errorf("invalid script result"))
	}

	decision := RateDecision{
		Granted:    values[0] == 1,
		ObservedAt: time.UnixMilli(values[1]).UTC(),
		Buckets:    make([]BucketState, len(limits)),
	}
	if !decision.Granted {
		if values[2] == 0 {
			return RateDecision{}, unavailable("acquire rate tokens", fmt.Errorf("missing retry delay"))
		}
		decision.RetryAt = decision.ObservedAt.Add(time.Duration(values[2]) * time.Millisecond)
	}
	for index, limit := range limits {
		if values[3+index] < 0 {
			return RateDecision{}, unavailable("acquire rate tokens", fmt.Errorf("negative bucket balance"))
		}
		decision.Buckets[index] = BucketState{Dimension: limit.Dimension, Metric: limit.Metric, RemainingTokens: values[3+index]}
	}
	return decision, nil
}

func (c *Coordinator) prepareRateLimits(limits []BucketLimit) ([]string, []any, error) {
	if len(limits) == 0 {
		return nil, nil, fmt.Errorf("%w: at least one bucket limit is required", ErrInvalidInput)
	}
	if len(limits) > maximumRateDimensions {
		return nil, nil, fmt.Errorf("%w: at most %d bucket limits are supported", ErrInvalidInput, maximumRateDimensions)
	}
	keys := make([]string, len(limits))
	arguments := make([]any, 0, len(limits)*5)
	seen := make(map[string]struct{}, len(limits))
	for index, limit := range limits {
		if err := validateDimension(limit.Dimension); err != nil {
			return nil, nil, err
		}
		if limit.Metric != MetricRequests && limit.Metric != MetricTokens {
			return nil, nil, fmt.Errorf("%w: unsupported bucket metric %q", ErrInvalidInput, limit.Metric)
		}
		if limit.CapacityTokens < 1 || limit.CapacityTokens > maximumBucketTokens ||
			limit.RefillTokens < 1 || limit.RefillTokens > maximumBucketTokens ||
			limit.RequestedTokens < 1 || limit.RequestedTokens > limit.CapacityTokens {
			return nil, nil, fmt.Errorf("%w: bucket token values are outside supported bounds", ErrInvalidInput)
		}
		if limit.RefillInterval < time.Millisecond || limit.RefillInterval > maximumRefillInterval {
			return nil, nil, fmt.Errorf("%w: refill interval must be between 1ms and 24h", ErrInvalidInput)
		}
		fullRefillMillis := math.Ceil(float64(limit.CapacityTokens) * float64(limit.RefillInterval.Milliseconds()) / float64(limit.RefillTokens))
		if fullRefillMillis > float64(maximumFullRefill.Milliseconds()) {
			return nil, nil, fmt.Errorf("%w: bucket full-refill duration exceeds 30 days", ErrInvalidInput)
		}
		idleTTLMillis := int64(math.Ceil(math.Max(float64(limit.RefillInterval.Milliseconds()), fullRefillMillis*2)))
		key := c.rateKey(limit)
		if _, exists := seen[key]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate bucket dimension and metric", ErrInvalidInput)
		}
		seen[key] = struct{}{}
		keys[index] = key
		arguments = append(arguments, limit.CapacityTokens, limit.RefillTokens, limit.RefillInterval.Milliseconds(), limit.RequestedTokens, idleTTLMillis)
	}
	return keys, arguments, nil
}
