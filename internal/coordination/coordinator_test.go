package coordination

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestAcquireRateUsesStableOpaqueKeysForEveryScope(t *testing.T) {
	serverTime := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	runner := &recordingScripter{result: scriptValues(1, serverTime.UnixMilli(), 0, 9, 9, 9, 9, 9, 9, 9, 9)}
	coordinator := mustCoordinator(t, runner, "scope-test")
	identifiers := []string{"", "pool-id", "person@example.com", "subscription-id", "llmg_secret-key", "model/body-text", "provider-id", "credential-id"}
	limits := []BucketLimit{
		rateLimit(GlobalDimension(), MetricRequests, 10),
		rateLimit(Dimension{Scope: ScopeResourcePool, SubjectID: identifiers[1]}, MetricRequests, 10),
		rateLimit(Dimension{Scope: ScopeUser, SubjectID: identifiers[2]}, MetricRequests, 10),
		rateLimit(Dimension{Scope: ScopeSubscription, SubjectID: identifiers[3]}, MetricTokens, 10),
		rateLimit(Dimension{Scope: ScopeGatewayKey, SubjectID: identifiers[4]}, MetricRequests, 10),
		rateLimit(Dimension{Scope: ScopeModel, SubjectID: identifiers[5]}, MetricTokens, 10),
		rateLimit(Dimension{Scope: ScopeProvider, SubjectID: identifiers[6]}, MetricRequests, 10),
		rateLimit(Dimension{Scope: ScopeCredential, SubjectID: identifiers[7]}, MetricRequests, 10),
	}

	decision, err := coordinator.AcquireRate(context.Background(), limits)
	if err != nil {
		t.Fatalf("AcquireRate() error = %v", err)
	}
	if !decision.Granted || !decision.ObservedAt.Equal(serverTime) || len(decision.Buckets) != len(limits) {
		t.Fatalf("AcquireRate() decision = %#v", decision)
	}
	if len(runner.keys) != len(limits) {
		t.Fatalf("script keys = %d, want %d", len(runner.keys), len(limits))
	}
	for _, key := range runner.keys {
		if !strings.Contains(key, "{coordination}") {
			t.Fatalf("coordination key %q does not share the atomic hash slot", key)
		}
		for _, identifier := range identifiers[1:] {
			if strings.Contains(key, identifier) {
				t.Fatalf("coordination key exposed caller identifier in %q", key)
			}
		}
	}

	secondRunner := &recordingScripter{result: runner.result}
	second := mustCoordinator(t, secondRunner, "scope-test")
	if _, err := second.AcquireRate(context.Background(), limits); err != nil {
		t.Fatalf("second AcquireRate() error = %v", err)
	}
	for index := range runner.keys {
		if runner.keys[index] != secondRunner.keys[index] {
			t.Fatalf("stable key %d changed across instances", index)
		}
	}
}

func TestCoordinationFailureFailsClosed(t *testing.T) {
	storeFailure := errors.New("Valkey connection refused")
	runner := &recordingScripter{err: storeFailure}
	coordinator := mustCoordinator(t, runner, "closed-test")
	decision, err := coordinator.AcquireRate(context.Background(), []BucketLimit{rateLimit(GlobalDimension(), MetricRequests, 1)})
	if err == nil || !errors.Is(err, ErrUnavailable) || !errors.Is(err, storeFailure) {
		t.Fatalf("AcquireRate() error = %v", err)
	}
	if decision.Granted {
		t.Fatal("AcquireRate() granted while coordination store failed")
	}
	lease, err := coordinator.AcquireLease(context.Background(), "request", time.Minute, []ConcurrencyLimit{{Dimension: GlobalDimension(), MaxInFlight: 1}})
	if err == nil || !errors.Is(err, ErrUnavailable) || lease.Granted {
		t.Fatalf("AcquireLease() = %#v, %v", lease, err)
	}
}

func TestLeaseKeysAndMembersDoNotExposeCallerValues(t *testing.T) {
	serverTime := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	runner := &recordingScripter{result: scriptValues(1, serverTime.UnixMilli(), serverTime.Add(time.Minute).UnixMilli(), 1)}
	coordinator := mustCoordinator(t, runner, "lease-key-test")
	leaseID := "sk-request-body-secret"
	subjectID := "person@example.com"
	decision, err := coordinator.AcquireLease(context.Background(), leaseID, time.Minute, []ConcurrencyLimit{{
		Dimension: Dimension{Scope: ScopeUser, SubjectID: subjectID}, MaxInFlight: 1,
	}})
	if err != nil || !decision.Granted {
		t.Fatalf("AcquireLease() = %#v, %v", decision, err)
	}
	wire := strings.Join(runner.keys, " ")
	for _, argument := range runner.args {
		wire += " " + fmt.Sprint(argument)
	}
	if strings.Contains(wire, leaseID) || strings.Contains(wire, subjectID) {
		t.Fatalf("Valkey script input exposed caller values: %s", wire)
	}
}

func TestCoordinatorRejectsAmbiguousLimits(t *testing.T) {
	coordinator := mustCoordinator(t, &recordingScripter{}, "validation-test")
	limit := rateLimit(GlobalDimension(), MetricRequests, 1)
	if _, err := coordinator.AcquireRate(context.Background(), []BucketLimit{limit, limit}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate AcquireRate() error = %v", err)
	}
	if _, err := coordinator.AcquireLease(context.Background(), "lease", time.Minute, []ConcurrencyLimit{{Dimension: GlobalDimension(), MaxInFlight: 1}, {Dimension: GlobalDimension(), MaxInFlight: 2}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate AcquireLease() error = %v", err)
	}
	if _, err := New(&recordingScripter{}, Options{Prefix: "bad prefix", KeyHashSecret: make([]byte, 32)}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("New() error = %v", err)
	}
}

func rateLimit(dimension Dimension, metric BucketMetric, capacity int64) BucketLimit {
	return BucketLimit{
		Dimension:       dimension,
		Metric:          metric,
		CapacityTokens:  capacity,
		RefillTokens:    capacity,
		RefillInterval:  time.Minute,
		RequestedTokens: 1,
	}
}

func mustCoordinator(t *testing.T, client redis.Scripter, prefix string) *Coordinator {
	t.Helper()
	coordinator, err := New(client, Options{Prefix: prefix, KeyHashSecret: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return coordinator
}

func scriptValues(values ...int64) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

type recordingScripter struct {
	result any
	err    error
	keys   []string
	args   []any
}

func (s *recordingScripter) command(ctx context.Context, keys []string, args ...any) *redis.Cmd {
	s.keys = append([]string(nil), keys...)
	s.args = append([]any(nil), args...)
	command := redis.NewCmd(ctx)
	command.SetVal(s.result)
	command.SetErr(s.err)
	return command
}

func (s *recordingScripter) Eval(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return s.command(ctx, keys, args...)
}

func (s *recordingScripter) EvalSha(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return s.command(ctx, keys, args...)
}

func (s *recordingScripter) EvalRO(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return s.command(ctx, keys, args...)
}

func (s *recordingScripter) EvalShaRO(ctx context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	return s.command(ctx, keys, args...)
}

func (s *recordingScripter) ScriptExists(ctx context.Context, _ ...string) *redis.BoolSliceCmd {
	return redis.NewBoolSliceCmd(ctx)
}

func (s *recordingScripter) ScriptLoad(ctx context.Context, _ string) *redis.StringCmd {
	return redis.NewStringCmd(ctx)
}
