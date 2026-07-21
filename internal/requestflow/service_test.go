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
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/resilience"
	"github.com/luckymaomi/llmgateway/internal/routing"
)

type workflowRepository struct {
	model          Model
	candidates     []Candidate
	statuses       []string
	attempts       []AttemptUpdate
	recoverable    []RecoverableSettlement
	staleQueued    []uuid.UUID
	recovered      int64
	recoveryEvents *[]string
	lastClaim      execution.Claim
	heartbeatErr   error
}

func (r *workflowRepository) ListPublishedModels(context.Context, uuid.UUID) ([]Model, error) {
	return []Model{r.model}, nil
}

func (r *workflowRepository) ResolvePublishedModel(_ context.Context, _ uuid.UUID, name string) (Model, error) {
	if name != r.model.PublicName {
		return Model{}, ErrModelNotFound
	}
	return r.model, nil
}

func (r *workflowRepository) ListPublishedCandidates(context.Context, uuid.UUID, uuid.UUID, registry.ResourceDomain) ([]Candidate, error) {
	return r.candidates, nil
}

func (r *workflowRepository) ClaimExecution(_ context.Context, requestID, executionID uuid.UUID) (execution.Claim, error) {
	r.statuses = append(r.statuses, "dispatching")
	r.lastClaim = execution.Claim{RequestID: requestID, ExecutionID: executionID, Generation: 1}
	return r.lastClaim, nil
}

func (r *workflowRepository) HeartbeatExecution(context.Context, execution.Claim) error {
	return r.heartbeatErr
}

func (r *workflowRepository) MarkExecutionStreaming(_ context.Context, _ execution.Claim, _ uuid.UUID, update AttemptUpdate) error {
	r.statuses = append(r.statuses, "streaming")
	r.attempts = append(r.attempts, update)
	return nil
}

func (r *workflowRepository) MarkExecutionUncertain(_ context.Context, _ execution.Claim, _ uuid.UUID, update AttemptUpdate, _, _ string) error {
	r.statuses = append(r.statuses, "uncertain")
	r.attempts = append(r.attempts, update)
	return nil
}

func (r *workflowRepository) RecoverStaleExecutions(context.Context, time.Time, int32) (int64, error) {
	if r.recoveryEvents != nil {
		*r.recoveryEvents = append(*r.recoveryEvents, "fence")
	}
	return r.recovered, nil
}

func (r *workflowRepository) ListRecoverableSettlements(context.Context, time.Time, int32) ([]RecoverableSettlement, error) {
	if r.recoveryEvents != nil {
		*r.recoveryEvents = append(*r.recoveryEvents, "list-settlements")
	}
	return r.recoverable, nil
}

func (r *workflowRepository) ListStaleQueuedRequests(context.Context, time.Time, int32) ([]uuid.UUID, error) {
	if r.recoveryEvents != nil {
		*r.recoveryEvents = append(*r.recoveryEvents, "list-queued")
	}
	return r.staleQueued, nil
}

func (r *workflowRepository) CreateAttempt(context.Context, execution.Claim, uuid.UUID, int) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (r *workflowRepository) UpdateAttempt(_ context.Context, _ execution.Claim, _ uuid.UUID, update AttemptUpdate) error {
	r.attempts = append(r.attempts, update)
	return nil
}

type workflowAccounting struct {
	accepted       Accepted
	settled        []Usage
	released       int
	compensated    []Usage
	acceptCalls    int
	acceptErr      error
	events         *[]string
	recoveryEvents *[]string
}

func (a *workflowAccounting) AcceptRequest(_ context.Context, command AcceptCommand) (Accepted, error) {
	a.acceptCalls++
	if a.events != nil {
		*a.events = append(*a.events, "reservation")
	}
	if a.acceptErr != nil {
		return Accepted{}, a.acceptErr
	}
	a.accepted.RequestID = command.RequestID
	return a.accepted, nil
}

func (a *workflowAccounting) Settle(_ context.Context, _ execution.Claim, usage Usage) error {
	if a.recoveryEvents != nil {
		*a.recoveryEvents = append(*a.recoveryEvents, "settle")
	}
	a.settled = append(a.settled, usage)
	return nil
}

func (a *workflowAccounting) Release(context.Context, execution.Claim, string, string) error {
	a.released++
	return nil
}

func (a *workflowAccounting) ReleaseAccepted(context.Context, uuid.UUID, string, string) error {
	if a.recoveryEvents != nil {
		*a.recoveryEvents = append(*a.recoveryEvents, "release")
	}
	a.released++
	return nil
}

func (a *workflowAccounting) Compensate(_ context.Context, _ execution.Claim, usage Usage, _ string) error {
	a.compensated = append(a.compensated, usage)
	return nil
}

type workflowSecrets struct{}

func (workflowSecrets) CredentialSecret(context.Context, uuid.UUID) (string, error) {
	return "upstream-secret", nil
}

type workflowLease struct{ context context.Context }

func (l workflowLease) Context() context.Context {
	if l.context == nil {
		return context.Background()
	}
	return l.context
}
func (workflowLease) Release(context.Context) error { return nil }

type workflowCoordinator struct {
	events   *[]string
	err      error
	requests *[]LeaseRequest
}

func (c workflowCoordinator) Acquire(ctx context.Context, request LeaseRequest) (Lease, time.Duration, error) {
	if c.events != nil {
		*c.events = append(*c.events, "capacity")
	}
	if c.requests != nil {
		*c.requests = append(*c.requests, request)
	}
	if c.err != nil {
		return nil, 0, c.err
	}
	return workflowLease{context: ctx}, 0, nil
}

type workflowAdmitter struct {
	events *[]string
	err    error
}

func (a workflowAdmitter) Acquire(context.Context, AdmissionRequest) (AdmissionPermit, time.Duration, error) {
	if a.events != nil {
		*a.events = append(*a.events, "admission")
	}
	if a.err != nil {
		return nil, 0, a.err
	}
	return workflowAdmissionPermit{}, 0, nil
}

type workflowAdmissionPermit struct{}

func (workflowAdmissionPermit) Release() {}

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

func TestChatTreatsTransportFailureAsUncertainAndHoldsReservation(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset after write")
	})}
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Kind != canonical.ErrorUncertain {
		t.Fatalf("error = %#v", providerError)
	}
	if len(accounting.compensated) != 0 || len(accounting.settled) != 0 || accounting.released != 0 {
		t.Fatalf("unexpected accounting terminal facts: %#v", accounting)
	}
	if got := strings.Join(repository.statuses, ","); got != "dispatching,uncertain" {
		t.Fatalf("request statuses = %s, want dispatching,uncertain", got)
	}
}

func TestChatDoesNotReplayAProviderFiveHundredResponse(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream failed","type":"server_error"}}`)),
		}, nil
	})}
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Kind != canonical.ErrorUncertain || requests != 1 {
		t.Fatalf("error/requests = %#v/%d", providerError, requests)
	}
	if got := strings.Join(repository.statuses, ","); got != "dispatching,uncertain" {
		t.Fatalf("request statuses = %s", got)
	}
	if accounting.released != 0 || len(accounting.settled) != 0 || len(accounting.compensated) != 0 {
		t.Fatalf("five hundred response wrote terminal accounting: %#v", accounting)
	}
}

func TestChatDoesNotReplayAMalformedSuccessfulResponse(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"incomplete"}`)),
		}, nil
	})}
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Kind != canonical.ErrorUncertain || requests != 1 {
		t.Fatalf("error/requests = %#v/%d", providerError, requests)
	}
	if got := strings.Join(repository.statuses, ","); got != "dispatching,uncertain" {
		t.Fatalf("request statuses = %s", got)
	}
	if accounting.released != 0 || len(accounting.settled) != 0 || len(accounting.compensated) != 0 {
		t.Fatalf("malformed success wrote terminal accounting: %#v", accounting)
	}
}

func TestChatRetriesAnExplicitRateLimitBeforeClientCommit(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit"}}`)),
			}, nil
		}
		return successfulRoundTrip(nil)
	})}
	service, _, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	if _, providerError := service.Chat(context.Background(), chatCommand(false)); providerError != nil {
		t.Fatalf("Chat returned error: %v", providerError)
	}
	if requests != 2 || len(accounting.settled) != 1 || accounting.released != 0 {
		t.Fatalf("rate-limit retry requests/accounting = %d/%#v", requests, accounting)
	}
}

func TestStreamPersistsStreamingBoundaryAndSettlesAuthoritativeUsage(t *testing.T) {
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamFixtureBody()))}, nil
	})})
	var events []canonical.StreamEventType
	streamError := service.Stream(context.Background(), chatCommand(true), func(_ uuid.UUID, event canonical.StreamEvent) error {
		events = append(events, event.Type)
		return nil
	})
	if streamError != nil {
		t.Fatalf("Stream returned error: %v", streamError)
	}
	if len(accounting.settled) != 1 || accounting.settled[0].InputTokens != 9 || accounting.settled[0].OutputTokens != 3 || accounting.settled[0].Source != canonical.UsageAuthoritative {
		t.Fatalf("stream settlement = %#v", accounting.settled)
	}
	if got := strings.Join(repository.statuses, ","); got != "dispatching,streaming" {
		t.Fatalf("request statuses = %s", got)
	}
	if len(repository.attempts) < 3 || repository.attempts[0].Status != "sending" || repository.attempts[1].Status != "streaming" || repository.attempts[len(repository.attempts)-1].Status != "completed" {
		t.Fatalf("stream attempt lifecycle = %#v", repository.attempts)
	}
	if len(events) == 0 || events[len(events)-1] != canonical.StreamDone {
		t.Fatalf("stream events = %#v", events)
	}
}

func TestStreamClientWriteFailureHoldsPartialExecutionAsUncertain(t *testing.T) {
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamFixtureBody()))}, nil
	})})
	streamError := service.Stream(context.Background(), chatCommand(true), func(_ uuid.UUID, event canonical.StreamEvent) error {
		if event.Type == canonical.StreamContentDelta {
			return errors.New("client connection closed")
		}
		return nil
	})
	if streamError == nil || streamError.Kind != canonical.ErrorStreamInterrupted {
		t.Fatalf("Stream error = %#v", streamError)
	}
	if accounting.released != 0 || len(accounting.settled) != 0 {
		t.Fatalf("partial stream wrote terminal accounting: %#v", accounting)
	}
	if got := strings.Join(repository.statuses, ","); got != "dispatching,streaming,uncertain" {
		t.Fatalf("request statuses = %s", got)
	}
	lastAttempt := repository.attempts[len(repository.attempts)-1]
	if lastAttempt.Status != "uncertain" || lastAttempt.Usage == nil || lastAttempt.Usage.Source != canonical.UsageEstimated {
		t.Fatalf("partial stream usage fact = %#v", lastAttempt)
	}
}

func TestStreamCancellationCancelsProviderAndHoldsUnknownExecution(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := &cancelableStreamBody{context: request.Context(), first: []byte(streamFirstEvent())}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}, nil
	})}
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	parent, cancel := context.WithCancel(context.Background())
	streamError := service.Stream(parent, chatCommand(true), func(_ uuid.UUID, event canonical.StreamEvent) error {
		if event.Type == canonical.StreamMessageStart {
			cancel()
		}
		return nil
	})
	if streamError == nil || streamError.Kind != canonical.ErrorStreamInterrupted {
		t.Fatalf("Stream cancellation error = %#v", streamError)
	}
	if accounting.released != 0 || len(accounting.settled) != 0 {
		t.Fatalf("stream cancellation wrote terminal accounting: %#v", accounting)
	}
	if got := strings.Join(repository.statuses, ","); got != "dispatching,streaming,uncertain" {
		t.Fatalf("request statuses = %s", got)
	}
}

func TestStreamDoesNotReplayProviderFiveHundredResponse(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"upstream failed","type":"server_error"}}`))}, nil
	})}
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	streamError := service.Stream(context.Background(), chatCommand(true), func(uuid.UUID, canonical.StreamEvent) error { return nil })
	if streamError == nil || streamError.Kind != canonical.ErrorUncertain || requests != 1 {
		t.Fatalf("stream error/requests = %#v/%d", streamError, requests)
	}
	if accounting.released != 0 || len(accounting.settled) != 0 || strings.Join(repository.statuses, ",") != "dispatching,uncertain" {
		t.Fatalf("stream five hundred facts = statuses %s accounting %#v", strings.Join(repository.statuses, ","), accounting)
	}
}

func streamFixtureBody() string {
	return streamFirstEvent() +
		"data: {\"id\":\"stream-1\",\"created\":1710000100,\"model\":\"upstream-glm\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"stream-1\",\"created\":1710000100,\"model\":\"upstream-glm\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":3,\"total_tokens\":12}}\n\n" +
		"data: [DONE]\n\n"
}

func streamFirstEvent() string {
	return "data: {\"id\":\"stream-1\",\"created\":1710000100,\"model\":\"upstream-glm\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"
}

type cancelableStreamBody struct {
	context context.Context
	first   []byte
	done    bool
}

func (b *cancelableStreamBody) Read(target []byte) (int, error) {
	if !b.done {
		b.done = true
		return copy(target, b.first), nil
	}
	<-b.context.Done()
	return 0, b.context.Err()
}

func (b *cancelableStreamBody) Close() error { return nil }

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

func TestChatAdmitsBeforeCreatingReservationAndCapacity(t *testing.T) {
	service, _, accounting := newWorkflowForTest(t, "https://provider.example/v1", &http.Client{Transport: roundTripFunc(successfulRoundTrip)})
	if _, providerError := service.Chat(context.Background(), chatCommand(false)); providerError != nil {
		t.Fatalf("Chat returned error: %v", providerError)
	}
	if got := strings.Join(*accounting.events, ","); got != "admission,reservation,capacity" {
		t.Fatalf("workflow events = %s, want admission,reservation,capacity", got)
	}
}

func TestAdmissionRejectionDoesNotCreateReservation(t *testing.T) {
	service, _, accounting := newWorkflowForTest(t, "https://provider.example/v1", http.DefaultClient)
	retryAt := time.Now().UTC().Add(time.Minute)
	service.admitter = workflowAdmitter{events: accounting.events, err: &CapacityError{RetryAt: retryAt}}
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Code != "admission_capacity_exhausted" {
		t.Fatalf("error = %#v", providerError)
	}
	if accounting.acceptCalls != 0 {
		t.Fatalf("accounting reservations = %d, want 0", accounting.acceptCalls)
	}
}

func TestCapacityRejectionReleasesAcceptedReservation(t *testing.T) {
	service, _, accounting := newWorkflowForTest(t, "https://provider.example/v1", http.DefaultClient)
	retryAt := time.Now().UTC().Add(time.Minute)
	service.coordinator = workflowCoordinator{events: accounting.events, err: &CapacityError{RetryAt: retryAt}}
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Code != "admission_capacity_exhausted" {
		t.Fatalf("error = %#v", providerError)
	}
	if accounting.acceptCalls != 1 || accounting.released != 1 {
		t.Fatalf("accounting accept/release = %d/%d, want 1/1", accounting.acceptCalls, accounting.released)
	}
}

func TestQuotaRejectionDoesNotConsumeProviderCapacity(t *testing.T) {
	service, _, accounting := newWorkflowForTest(t, "https://provider.example/v1", http.DefaultClient)
	accounting.acceptErr = ErrQuotaExhausted
	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Code != "quota_exhausted" {
		t.Fatalf("error = %#v", providerError)
	}
	if got := strings.Join(*accounting.events, ","); got != "admission,reservation" {
		t.Fatalf("workflow events = %s, want admission,reservation", got)
	}
}

func TestRecoverySettlesKnownUsageBeforeReleasingQueuedAndFencingUnknownExecutions(t *testing.T) {
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", http.DefaultClient)
	events := []string{}
	requestID := uuid.New()
	repository.recoveryEvents = &events
	repository.recoverable = []RecoverableSettlement{{
		Claim: execution.Claim{RequestID: requestID, ExecutionID: uuid.New(), Generation: 3},
		Usage: Usage{InputTokens: 7, OutputTokens: 2, Source: canonical.UsageAuthoritative},
	}}
	repository.staleQueued = []uuid.UUID{uuid.New()}
	repository.recovered = 1
	accounting.recoveryEvents = &events

	result, err := service.RecoverOnce(context.Background(), time.Now().UTC().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("RecoverOnce() error = %v", err)
	}
	if result != (RecoveryResult{Settled: 1, Released: 1, Uncertain: 1}) {
		t.Fatalf("recovery result = %#v", result)
	}
	if got := strings.Join(events, ","); got != "list-settlements,settle,list-queued,release,fence" {
		t.Fatalf("recovery order = %s", got)
	}
}

func TestCoordinationLeaseUsesTheClaimedExecutionIdentity(t *testing.T) {
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", &http.Client{Transport: roundTripFunc(successfulRoundTrip)})
	rpmLimit := int32(7)
	tpmLimit := int64(700)
	accounting.accepted.EntitlementRPMLimit = &rpmLimit
	accounting.accepted.EntitlementTPMLimit = &tpmLimit
	requests := []LeaseRequest{}
	service.coordinator = workflowCoordinator{requests: &requests}
	if _, providerError := service.Chat(context.Background(), chatCommand(false)); providerError != nil {
		t.Fatalf("Chat returned error: %v", providerError)
	}
	if len(requests) != 1 || requests[0].RequestID != repository.lastClaim.RequestID || requests[0].ExecutionID != repository.lastClaim.ExecutionID || requests[0].ExecutionID == requests[0].RequestID {
		t.Fatalf("lease request = %#v, claim = %#v", requests, repository.lastClaim)
	}
	if requests[0].ResourceDomain != registry.ResourceFree || requests[0].EntitlementID != accounting.accepted.EntitlementID || requests[0].EntitlementConcurrency != accounting.accepted.EntitlementConcurrency || requests[0].EntitlementRPMLimit == nil || *requests[0].EntitlementRPMLimit != rpmLimit || requests[0].EntitlementTPMLimit == nil || *requests[0].EntitlementTPMLimit != tpmLimit {
		t.Fatalf("lease request omitted accepted entitlement capacity: %#v", requests[0])
	}
}

func TestHeartbeatFailureCancelsTheProviderAndPersistsUncertain(t *testing.T) {
	providerCanceled := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		close(providerCanceled)
		return nil, request.Context().Err()
	})}
	service, repository, accounting := newWorkflowForTest(t, "https://provider.example/v1", client)
	repository.heartbeatErr = errors.New("postgres heartbeat unavailable")
	service.config.ExecutionHeartbeatInterval = time.Millisecond

	_, providerError := service.Chat(context.Background(), chatCommand(false))
	if providerError == nil || providerError.Kind != canonical.ErrorUncertain {
		t.Fatalf("Chat error = %#v", providerError)
	}
	select {
	case <-providerCanceled:
	default:
		t.Fatal("provider request was not canceled after heartbeat failure")
	}
	if got := strings.Join(repository.statuses, ","); got != "dispatching,uncertain" {
		t.Fatalf("request statuses = %s", got)
	}
	if accounting.released != 0 || len(accounting.settled) != 0 || len(accounting.compensated) != 0 {
		t.Fatalf("heartbeat failure wrote a terminal accounting fact: %#v", accounting)
	}
}

func successfulRoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl-order","model":"upstream-glm","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
	}, nil
}

func newWorkflowForTest(t *testing.T, baseURL string, client *http.Client) (*Service, *workflowRepository, *workflowAccounting) {
	t.Helper()
	capabilities := providers.NarrowOpenAICompatibleCapabilities()
	adapter, err := providers.NewOpenAICompatible(providers.OpenAICompatibleOptions{BaseURL: baseURL, Capabilities: capabilities})
	if err != nil {
		t.Fatal(err)
	}
	modelID := uuid.New()
	revisionID := uuid.New()
	repository := &workflowRepository{
		model: Model{
			ConfigRevisionID: revisionID, ID: modelID, PublicName: "public-glm", UpstreamName: "upstream-glm", ProviderID: uuid.New(),
			ProviderKind: providers.KindOpenAICompatible, ProviderBaseURL: baseURL, ResourceDomain: registry.ResourceFree,
			Capabilities: registry.ModelCapabilities{Chat: true, Streaming: true, Tools: true, Reasoning: true, StructuredOutput: true, ContextTokens: 8192, OutputTokens: 2048},
		},
		candidates: []Candidate{{ID: uuid.New(), Priority: 10, Weight: 100}},
	}
	events := []string{}
	accounting := &workflowAccounting{accepted: Accepted{
		ReservationID: uuid.New(), EntitlementID: uuid.New(), EntitlementConcurrency: 2,
	}, events: &events}
	clock := fixedClock{now: time.Unix(100, 0).UTC()}
	router, err := routing.NewRouter(zeroRandom{})
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
	service, err := New(repository, accounting, workflowSecrets{}, workflowAdmitter{events: &events}, workflowCoordinator{events: &events}, workflowFactory{adapter: adapter, client: client}, router, retry, clock, Config{
		MaxResponseBytes:           1 << 20,
		ExecutionHeartbeatInterval: time.Hour,
		Circuit:                    resilience.CircuitConfig{FailureThreshold: 3, SuccessThreshold: 1, OpenDuration: time.Minute, HalfOpenMaxInFlight: 1},
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
