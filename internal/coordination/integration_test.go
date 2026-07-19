package coordination

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestValkeyRateAcquisitionIsAtomicAcrossDimensions(t *testing.T) {
	coordinator, _, _ := integrationCoordinator(t)
	global := BucketLimit{
		Dimension: GlobalDimension(), Metric: MetricRequests,
		CapacityTokens: 2, RefillTokens: 2, RefillInterval: time.Hour, RequestedTokens: 1,
	}
	user := BucketLimit{
		Dimension: Dimension{Scope: ScopeUser, SubjectID: "user-42"}, Metric: MetricRequests,
		CapacityTokens: 1, RefillTokens: 1, RefillInterval: time.Hour, RequestedTokens: 1,
	}

	first, err := coordinator.AcquireRate(context.Background(), []BucketLimit{global, user})
	if err != nil || !first.Granted {
		t.Fatalf("first AcquireRate() = %#v, %v", first, err)
	}
	second, err := coordinator.AcquireRate(context.Background(), []BucketLimit{global, user})
	if err != nil {
		t.Fatalf("second AcquireRate() error = %v", err)
	}
	if second.Granted || !second.RetryAt.After(second.ObservedAt) {
		t.Fatalf("second AcquireRate() = %#v", second)
	}

	globalOnly, err := coordinator.AcquireRate(context.Background(), []BucketLimit{global})
	if err != nil || !globalOnly.Granted || globalOnly.Buckets[0].RemainingTokens != 0 {
		t.Fatalf("global-only AcquireRate() = %#v, %v; denied multi-bucket call deducted another dimension", globalOnly, err)
	}
}

func TestValkeyRateRetryDeadlineComesFromServerTime(t *testing.T) {
	coordinator, _, _ := integrationCoordinator(t)
	limit := BucketLimit{
		Dimension: Dimension{Scope: ScopeGatewayKey, SubjectID: "gateway-key-3"}, Metric: MetricTokens,
		CapacityTokens: 1, RefillTokens: 1, RefillInterval: 40 * time.Millisecond, RequestedTokens: 1,
	}
	first, err := coordinator.AcquireRate(context.Background(), []BucketLimit{limit})
	if err != nil || !first.Granted {
		t.Fatalf("first AcquireRate() = %#v, %v", first, err)
	}
	blocked, err := coordinator.AcquireRate(context.Background(), []BucketLimit{limit})
	if err != nil || blocked.Granted || !blocked.RetryAt.After(blocked.ObservedAt) {
		t.Fatalf("blocked AcquireRate() = %#v, %v", blocked, err)
	}
	granted := waitForRateGrant(t, coordinator, limit, blocked.RetryAt)
	if !granted.Granted || granted.Buckets[0].RemainingTokens != 0 {
		t.Fatalf("AcquireRate(after retry deadline) = %#v", granted)
	}
}

func TestValkeyLeaseIsSharedRenewableAndIdempotentlyReleased(t *testing.T) {
	firstInstance, client, prefix := integrationCoordinator(t)
	secret := sha256.Sum256([]byte("coordination-integration:" + prefix))
	secondInstance, err := New(client, Options{Prefix: prefix, KeyHashSecret: secret[:]})
	if err != nil {
		t.Fatalf("New(second instance) error = %v", err)
	}
	limits := []ConcurrencyLimit{
		{Dimension: GlobalDimension(), MaxInFlight: 2},
		{Dimension: Dimension{Scope: ScopeCredential, SubjectID: "credential-7"}, MaxInFlight: 1},
	}

	first, err := firstInstance.AcquireLease(context.Background(), "request-a", time.Second, limits)
	if err != nil || !first.Granted {
		t.Fatalf("first AcquireLease() = %#v, %v", first, err)
	}
	blocked, err := secondInstance.AcquireLease(context.Background(), "request-b", time.Second, limits)
	if err != nil || blocked.Granted || !blocked.RetryAt.After(blocked.ObservedAt) {
		t.Fatalf("blocked AcquireLease() = %#v, %v", blocked, err)
	}
	globalOnly, err := secondInstance.AcquireLease(context.Background(), "request-c", time.Second, []ConcurrencyLimit{{Dimension: GlobalDimension(), MaxInFlight: 2}})
	if err != nil || !globalOnly.Granted {
		t.Fatalf("global-only AcquireLease() = %#v, %v; denied multi-dimension call occupied another dimension", globalOnly, err)
	}
	if err := secondInstance.ReleaseLease(context.Background(), globalOnly.Lease); err != nil {
		t.Fatalf("ReleaseLease(global-only) error = %v", err)
	}
	renewedUntil, err := secondInstance.RenewLease(context.Background(), first.Lease, 2*time.Second)
	if err != nil || !renewedUntil.After(first.ExpiresAt) {
		t.Fatalf("RenewLease() = %s, %v", renewedUntil, err)
	}
	if err := secondInstance.ReleaseLease(context.Background(), first.Lease); err != nil {
		t.Fatalf("ReleaseLease() error = %v", err)
	}
	if err := firstInstance.ReleaseLease(context.Background(), first.Lease); err != nil {
		t.Fatalf("repeated ReleaseLease() error = %v", err)
	}

	next, err := secondInstance.AcquireLease(context.Background(), "request-b", time.Second, limits)
	if err != nil || !next.Granted {
		t.Fatalf("next AcquireLease() = %#v, %v", next, err)
	}
}

func TestValkeyExpiredLeaseIsCleanedAndCannotBeRenewed(t *testing.T) {
	coordinator, _, _ := integrationCoordinator(t)
	limits := []ConcurrencyLimit{{Dimension: Dimension{Scope: ScopeModel, SubjectID: "model-9"}, MaxInFlight: 1}}
	first, err := coordinator.AcquireLease(context.Background(), "expires", 40*time.Millisecond, limits)
	if err != nil || !first.Granted {
		t.Fatalf("AcquireLease() = %#v, %v", first, err)
	}
	blocked, err := coordinator.AcquireLease(context.Background(), "after-expiry", time.Second, limits)
	if err != nil || blocked.Granted {
		t.Fatalf("blocked AcquireLease() = %#v, %v", blocked, err)
	}

	cleanup := waitForExpiredCleanup(t, coordinator, limits, blocked.RetryAt)
	if cleanup.Removed != 1 || cleanup.InUse[0].InUse != 0 {
		t.Fatalf("CleanupExpiredLeases() = %#v", cleanup)
	}
	if _, err := coordinator.RenewLease(context.Background(), first.Lease, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("RenewLease(expired) error = %v", err)
	}
	next, err := coordinator.AcquireLease(context.Background(), "after-expiry", time.Second, limits)
	if err != nil || !next.Granted {
		t.Fatalf("AcquireLease(after cleanup) = %#v, %v", next, err)
	}
}

func waitForExpiredCleanup(t *testing.T, coordinator *Coordinator, limits []ConcurrencyLimit, retryAt time.Time) CleanupResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		result, err := coordinator.CleanupExpiredLeases(ctx, limits)
		if err != nil {
			t.Fatalf("CleanupExpiredLeases() error = %v", err)
		}
		if result.Removed > 0 {
			return result
		}
		wait := time.Until(retryAt)
		if wait <= 0 || wait > 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("lease did not expire before deadline: %v", ctx.Err())
		case <-timer.C:
		}
	}
}

func waitForRateGrant(t *testing.T, coordinator *Coordinator, limit BucketLimit, retryAt time.Time) RateDecision {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		decision, err := coordinator.AcquireRate(ctx, []BucketLimit{limit})
		if err != nil {
			t.Fatalf("AcquireRate() error = %v", err)
		}
		if decision.Granted {
			return decision
		}
		if decision.RetryAt.After(retryAt) {
			retryAt = decision.RetryAt
		}
		wait := time.Until(retryAt)
		if wait <= 0 || wait > 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("rate bucket did not refill before deadline: %v", ctx.Err())
		case <-timer.C:
		}
	}
}

func integrationCoordinator(t *testing.T) (*Coordinator, *redis.Client, string) {
	t.Helper()
	address := environmentOr("LLMGATEWAY_TEST_VALKEY_ADDRESS", "127.0.0.1:16380")
	password := environmentOr("LLMGATEWAY_TEST_VALKEY_PASSWORD", "llmgateway_dev")
	database, err := strconv.Atoi(environmentOr("LLMGATEWAY_TEST_VALKEY_DATABASE", "0"))
	if err != nil {
		t.Fatalf("invalid LLMGATEWAY_TEST_VALKEY_DATABASE: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: password, DB: database, DialTimeout: 500 * time.Millisecond})
	pingContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(pingContext).Err(); err != nil {
		_ = client.Close()
		if os.Getenv("LLMGATEWAY_TEST_VALKEY_REQUIRED") == "true" {
			t.Fatalf("Valkey integration dependency is required: %v", err)
		}
		t.Skipf("Valkey integration dependency is unavailable: %v", err)
	}
	id, err := NewLeaseID()
	if err != nil {
		t.Fatalf("NewLeaseID() error = %v", err)
	}
	prefix := "llmgateway-it-" + id
	secret := sha256.Sum256([]byte("coordination-integration:" + prefix))
	coordinator, err := New(client, Options{Prefix: prefix, KeyHashSecret: secret[:]})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		var cursor uint64
		for {
			keys, next, scanErr := client.Scan(cleanupContext, cursor, prefix+":*", 100).Result()
			if scanErr != nil {
				t.Errorf("scan integration keys: %v", scanErr)
				break
			}
			if len(keys) > 0 {
				if deleteErr := client.Del(cleanupContext, keys...).Err(); deleteErr != nil {
					t.Errorf("delete integration keys: %v", deleteErr)
					break
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("close Valkey client: %v", closeErr)
		}
	})
	return coordinator, client, prefix
}

func environmentOr(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
