package coordination

import (
	"context"
	"fmt"
	"time"
)

const (
	maximumLeaseTTL         = 24 * time.Hour
	maximumConcurrencyLimit = int64(1_000_000_000)
	maximumLeaseDimensions  = 8
)

// AcquireLease atomically acquires all supplied concurrency dimensions. A
// repeated call with the same ID and dimensions renews the existing lease.
func (c *Coordinator) AcquireLease(ctx context.Context, leaseID string, ttl time.Duration, limits []ConcurrencyLimit) (LeaseDecision, error) {
	keys, dimensions, arguments, err := c.prepareLeaseAcquire(leaseID, ttl, limits)
	if err != nil {
		return LeaseDecision{}, err
	}
	values, err := acquireLeaseScript.Run(ctx, c.client, keys, arguments...).Int64Slice()
	if err != nil {
		return LeaseDecision{}, unavailable("acquire concurrency lease", err)
	}
	if len(values) != 3+len(limits) || (values[0] != 0 && values[0] != 1) || values[1] <= 0 || values[2] <= 0 {
		return LeaseDecision{}, unavailable("acquire concurrency lease", fmt.Errorf("invalid script result"))
	}

	decision := LeaseDecision{
		Granted:    values[0] == 1,
		ObservedAt: time.UnixMilli(values[1]).UTC(),
		Dimensions: make([]ConcurrencyState, len(limits)),
	}
	if decision.Granted {
		decision.Lease = LeaseRef{ID: leaseID, Dimensions: append([]Dimension(nil), dimensions...)}
		decision.ExpiresAt = time.UnixMilli(values[2]).UTC()
	} else {
		decision.RetryAt = decision.ObservedAt.Add(time.Duration(values[2]) * time.Millisecond)
	}
	for index, limit := range limits {
		if values[3+index] < 0 {
			return LeaseDecision{}, unavailable("acquire concurrency lease", fmt.Errorf("negative lease count"))
		}
		decision.Dimensions[index] = ConcurrencyState{Dimension: limit.Dimension, InUse: values[3+index], Limit: limit.MaxInFlight}
	}
	return decision, nil
}

// RenewLease extends a lease only while every original dimension is held.
func (c *Coordinator) RenewLease(ctx context.Context, lease LeaseRef, ttl time.Duration) (time.Time, error) {
	keys, err := c.prepareLeaseRef(lease, ttl)
	if err != nil {
		return time.Time{}, err
	}
	values, err := renewLeaseScript.Run(ctx, c.client, keys, ttl.Milliseconds(), c.leaseMember(lease.ID), leaseKeyGraceMilliseconds).Int64Slice()
	if err != nil {
		return time.Time{}, unavailable("renew concurrency lease", err)
	}
	if len(values) != 3 || (values[0] != 0 && values[0] != 1) || values[1] <= 0 {
		return time.Time{}, unavailable("renew concurrency lease", fmt.Errorf("invalid script result"))
	}
	if values[0] == 0 {
		return time.Time{}, ErrLeaseLost
	}
	if values[2] <= values[1] {
		return time.Time{}, unavailable("renew concurrency lease", fmt.Errorf("invalid expiration"))
	}
	return time.UnixMilli(values[2]).UTC(), nil
}

// ReleaseLease is idempotent. Expiration or a repeated release is successful.
func (c *Coordinator) ReleaseLease(ctx context.Context, lease LeaseRef) error {
	keys, err := c.prepareLeaseRef(lease, time.Millisecond)
	if err != nil {
		return err
	}
	values, err := releaseLeaseScript.Run(ctx, c.client, keys, c.leaseMember(lease.ID), leaseKeyGraceMilliseconds).Int64Slice()
	if err != nil {
		return unavailable("release concurrency lease", err)
	}
	if len(values) != 2 || values[0] <= 0 || values[1] < 0 {
		return unavailable("release concurrency lease", fmt.Errorf("invalid script result"))
	}
	return nil
}

// CleanupExpiredLeases removes crashed-request leases using Valkey server time.
func (c *Coordinator) CleanupExpiredLeases(ctx context.Context, limits []ConcurrencyLimit) (CleanupResult, error) {
	keys, _, err := c.prepareConcurrencyLimits(limits)
	if err != nil {
		return CleanupResult{}, err
	}
	values, err := cleanupLeaseScript.Run(ctx, c.client, keys, leaseKeyGraceMilliseconds).Int64Slice()
	if err != nil {
		return CleanupResult{}, unavailable("clean expired concurrency leases", err)
	}
	if len(values) != 2+len(limits) || values[0] <= 0 || values[1] < 0 {
		return CleanupResult{}, unavailable("clean expired concurrency leases", fmt.Errorf("invalid script result"))
	}
	result := CleanupResult{ObservedAt: time.UnixMilli(values[0]).UTC(), Removed: values[1], InUse: make([]ConcurrencyState, len(limits))}
	for index, limit := range limits {
		if values[2+index] < 0 {
			return CleanupResult{}, unavailable("clean expired concurrency leases", fmt.Errorf("negative lease count"))
		}
		result.InUse[index] = ConcurrencyState{Dimension: limit.Dimension, InUse: values[2+index], Limit: limit.MaxInFlight}
	}
	return result, nil
}

func (c *Coordinator) prepareLeaseAcquire(leaseID string, ttl time.Duration, limits []ConcurrencyLimit) ([]string, []Dimension, []any, error) {
	if err := validateLeaseIDAndTTL(leaseID, ttl); err != nil {
		return nil, nil, nil, err
	}
	keys, dimensions, err := c.prepareConcurrencyLimits(limits)
	if err != nil {
		return nil, nil, nil, err
	}
	arguments := make([]any, 0, 3+len(limits))
	arguments = append(arguments, ttl.Milliseconds(), c.leaseMember(leaseID), leaseKeyGraceMilliseconds)
	for _, limit := range limits {
		arguments = append(arguments, limit.MaxInFlight)
	}
	return keys, dimensions, arguments, nil
}

func (c *Coordinator) prepareLeaseRef(lease LeaseRef, ttl time.Duration) ([]string, error) {
	if err := validateLeaseIDAndTTL(lease.ID, ttl); err != nil {
		return nil, err
	}
	if len(lease.Dimensions) == 0 {
		return nil, fmt.Errorf("%w: at least one lease dimension is required", ErrInvalidInput)
	}
	if len(lease.Dimensions) > maximumLeaseDimensions {
		return nil, fmt.Errorf("%w: at most %d lease dimensions are supported", ErrInvalidInput, maximumLeaseDimensions)
	}
	keys := make([]string, len(lease.Dimensions))
	seen := make(map[string]struct{}, len(lease.Dimensions))
	for index, dimension := range lease.Dimensions {
		if err := validateDimension(dimension); err != nil {
			return nil, err
		}
		key := c.leaseKey(dimension)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate lease dimension", ErrInvalidInput)
		}
		seen[key] = struct{}{}
		keys[index] = key
	}
	return keys, nil
}

func (c *Coordinator) prepareConcurrencyLimits(limits []ConcurrencyLimit) ([]string, []Dimension, error) {
	if len(limits) == 0 {
		return nil, nil, fmt.Errorf("%w: at least one concurrency limit is required", ErrInvalidInput)
	}
	if len(limits) > maximumLeaseDimensions {
		return nil, nil, fmt.Errorf("%w: at most %d concurrency limits are supported", ErrInvalidInput, maximumLeaseDimensions)
	}
	keys := make([]string, len(limits))
	dimensions := make([]Dimension, len(limits))
	seen := make(map[string]struct{}, len(limits))
	for index, limit := range limits {
		if err := validateDimension(limit.Dimension); err != nil {
			return nil, nil, err
		}
		if limit.MaxInFlight < 1 || limit.MaxInFlight > maximumConcurrencyLimit {
			return nil, nil, fmt.Errorf("%w: concurrency limit is outside supported bounds", ErrInvalidInput)
		}
		key := c.leaseKey(limit.Dimension)
		if _, exists := seen[key]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate concurrency dimension", ErrInvalidInput)
		}
		seen[key] = struct{}{}
		keys[index] = key
		dimensions[index] = limit.Dimension
	}
	return keys, dimensions, nil
}

func validateLeaseIDAndTTL(leaseID string, ttl time.Duration) error {
	if len(leaseID) == 0 || len(leaseID) > maximumSubjectBytes {
		return fmt.Errorf("%w: lease ID must contain 1-%d bytes", ErrInvalidInput, maximumSubjectBytes)
	}
	if ttl < time.Millisecond || ttl > maximumLeaseTTL {
		return fmt.Errorf("%w: lease TTL must be between 1ms and 24h", ErrInvalidInput)
	}
	return nil
}
