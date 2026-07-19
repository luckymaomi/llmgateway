package requestflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/resilience"
	"github.com/luckymaomi/llmgateway/internal/routing"
)

type workflowRepository struct {
	model      Model
	candidates []Candidate
	attempts   []AttemptUpdate
}

func (r *workflowRepository) ListAuthorizedModels(context.Context, uuid.UUID) ([]Model, error) {
	return []Model{r.model}, nil
}

func (r *workflowRepository) ResolveAuthorizedModel(_ context.Context, _ uuid.UUID, name string) (Model, error) {
	if name != r.model.PublicName {
		return Model{}, ErrModelNotFound
	}
	return r.model, nil
}

func (r *workflowRepository) ListCandidates(context.Context, uuid.UUID, registry.ResourceDomain) ([]Candidate, error) {
	return r.candidates, nil
}

func (r *workflowRepository) ActiveConfigRevision(context.Context) (*uuid.UUID, error) {
	return nil, nil
}

func (r *workflowRepository) CreateAttempt(context.Context, uuid.UUID, uuid.UUID, int) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (r *workflowRepository) UpdateAttempt(_ context.Context, _ uuid.UUID, update AttemptUpdate) error {
	r.attempts = append(r.attempts, update)
	return nil
}

type workflowAccounting struct {
	accepted    Accepted
	settled     []Usage
	released    int
	compensated []Usage
}

func (a *workflowAccounting) AcceptRequest(context.Context, AcceptCommand) (Accepted, error) {
	return a.accepted, nil
}

func (a *workflowAccounting) Settle(_ context.Context, _ uuid.UUID, usage Usage) error {
	a.settled = append(a.settled, usage)
	return nil
}

func (a *workflowAccounting) Release(context.Context, uuid.UUID, string, string) error {
	a.released++
	return nil
}

func (a *workflowAccounting) Compensate(_ context.Context, _ uuid.UUID, usage Usage, _ string) error {
	a.compensated = append(a.compensated, usage)
	return nil
}

type workflowSecrets struct{}

func (workflowSecrets) CredentialSecret(context.Context, uuid.UUID) (string, error) {
	return "upstream-secret", nil
}

type workflowLease struct{}

func (workflowLease) Release(context.Context) error { return nil }

type workflowCoordinator struct{}

func (workflowCoordinator) Acquire(context.Context, LeaseRequest) (Lease, time.Duration, error) {
	return workflowLease{}, 0, nil
}

type workflowFactory struct {
	adapter providers.Adapter
	client  *http.Client
}

func (f workflowFactory) Adapter(Model) (providers.Adapter, error) { return f.adapter, nil }
func (f workflowFactory) Client(Candidate) (*http.Client, error)   { return f.client, nil }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type zeroRandom struct{}

func (zeroRandom) Intn(int) int       { return 0 }
func (zeroRandom) Int63n(int64) int64 { return 0 }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestChatPersistsAttemptAndSettlesAuthoritativeUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Fatalf("authorization header was not forwarded")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"upstream-glm"`) {
			t.Fatalf("upstream model not translated: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","model":"upstream-glm","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}`)
	}))
	defer server.Close()

	service, repository, accounting := newWorkflowForTest(t, server.URL, server.Client())
	result, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError != nil {
		t.Fatalf("Chat returned error: %v", providerError)
	}
	if result.Response.Model != "public-glm" || len(accounting.settled) != 1 {
		t.Fatalf("unexpected result or settlement: %#v %#v", result.Response, accounting.settled)
	}
	if usage := accounting.settled[0]; usage.InputTokens != 9 || usage.OutputTokens != 3 || usage.Source != canonical.UsageAuthoritative {
		t.Fatalf("unexpected settled usage: %#v", usage)
	}
	if len(repository.attempts) != 2 || repository.attempts[0].Status != "sending" || repository.attempts[1].Status != "completed" {
		t.Fatalf("attempt lifecycle = %#v", repository.attempts)
	}
}

func TestChatTreatsTransportFailureAsUncertainAndCompensates(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset after write")
	})}
	service, _, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Kind != canonical.ErrorUncertain {
		t.Fatalf("error = %#v", providerError)
	}
	if len(accounting.compensated) != 1 || len(accounting.settled) != 0 || accounting.released != 0 {
		t.Fatalf("unexpected accounting terminal facts: %#v", accounting)
	}
}

func TestExistingIdempotentRequestDoesNotDispatchAgain(t *testing.T) {
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", http.DefaultClient)
	accounting.accepted.Existing = true
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.HTTPStatus != http.StatusConflict {
		t.Fatalf("error = %#v", providerError)
	}
	if len(repository.attempts) != 0 {
		t.Fatalf("duplicate request created attempts: %#v", repository.attempts)
	}
}

func newWorkflowForTest(t *testing.T, baseURL string, client *http.Client) (*Service, *workflowRepository, *workflowAccounting) {
	t.Helper()
	capabilities := providers.NarrowOpenAICompatibleCapabilities()
	adapter, err := providers.NewOpenAICompatible(providers.OpenAICompatibleOptions{BaseURL: baseURL, Capabilities: capabilities})
	if err != nil {
		t.Fatal(err)
	}
	modelID := uuid.New()
	repository := &workflowRepository{
		model: Model{
			ID: modelID, PublicName: "public-glm", UpstreamName: "upstream-glm", ProviderID: uuid.New(),
			ProviderKind: providers.KindOpenAICompatible, ProviderBaseURL: baseURL, ResourceDomain: registry.ResourceFree,
			Capabilities: registry.ModelCapabilities{Chat: true, Streaming: true, Tools: true, Reasoning: true, StructuredOutput: true, ContextTokens: 8192, OutputTokens: 2048},
		},
		candidates: []Candidate{{ID: uuid.New(), Priority: 10, Weight: 100}},
	}
	accounting := &workflowAccounting{accepted: Accepted{RequestID: uuid.New(), ReservationID: uuid.New()}}
	clock := fixedClock{now: time.Unix(100, 0).UTC()}
	router, err := routing.NewRouter(routing.Policy{
		Weights: routing.Weights{Priority: 1, Load: 1, Reliability: 1}, TTFTCeiling: time.Second, LatencyCeiling: time.Minute,
	}, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := resilience.NewRetryPolicy(resilience.RetryConfig{
		MaxAttempts: 2, MaxElapsed: time.Minute,
		Backoff: resilience.BackoffConfig{Initial: time.Nanosecond, Maximum: time.Nanosecond, MultiplierNumerator: 1, MultiplierDenominator: 1},
	}, clock, zeroRandom{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(repository, accounting, workflowSecrets{}, workflowCoordinator{}, workflowFactory{adapter: adapter, client: client}, router, retry, clock, Config{
		MaxResponseBytes: 1 << 20,
		Circuit:          resilience.CircuitConfig{FailureThreshold: 3, SuccessThreshold: 1, OpenDuration: time.Minute, HalfOpenMaxInFlight: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, accounting
}

func chatCommand(stream bool) ChatCommand {
	maxTokens := int64(64)
	return ChatCommand{
		Principal:     identity.GatewayPrincipal{UserID: uuid.New(), KeyID: uuid.New()},
		Request:       canonical.ChatRequest{Model: "public-glm", Stream: stream, MaxOutputTokens: &maxTokens, Messages: []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("hi")}}},
		RequestDigest: []byte("digest"),
	}
}
