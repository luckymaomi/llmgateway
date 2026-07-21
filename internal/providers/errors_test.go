package providers

import (
	"net/http"
	"testing"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func TestProviderErrorFixturesProduceStableKinds(t *testing.T) {
	t.Parallel()

	compatible, err := NewOpenAICompatible(OpenAICompatibleOptions{
		BaseURL: "https://llm.example/v1", Capabilities: NarrowOpenAICompatibleCapabilities(),
	})
	if err != nil {
		t.Fatalf("create compatible adapter: %v", err)
	}
	tests := []struct {
		name       string
		adapter    Adapter
		statusCode int
		body       string
		wantKind   canonical.ErrorKind
		wantCode   string
	}{
		{
			name: "Zhipu platform overload", adapter: NewZhipu(), statusCode: http.StatusTooManyRequests,
			body:     `{"error":{"code":"1305","message":"Model traffic is high"}}`,
			wantKind: canonical.ErrorProviderTemporary, wantCode: "1305",
		},
		{
			name: "Zhipu account rate limit", adapter: NewZhipu(), statusCode: http.StatusTooManyRequests,
			body:     `{"error":{"code":1302,"message":"Rate limit reached"}}`,
			wantKind: canonical.ErrorRateLimit, wantCode: "1302",
		},
		{
			name: "Agnes authentication", adapter: NewAgnes(), statusCode: http.StatusUnauthorized,
			body:     `{"error":{"message":"Authentication failed","type":"authentication_error","code":"invalid_api_key"}}`,
			wantKind: canonical.ErrorAuthentication, wantCode: "invalid_api_key",
		},
		{
			name: "Compatible invalid parameters", adapter: compatible, statusCode: http.StatusUnprocessableEntity,
			body:     `{"error":{"message":"Invalid parameter","type":"invalid_request_error","param":"max_tokens","code":"invalid_value"}}`,
			wantKind: canonical.ErrorInvalidRequest, wantCode: "invalid_value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			headers := http.Header{"Retry-After": []string{"17"}}
			classified := test.adapter.ClassifyError(test.statusCode, headers, []byte(test.body))
			if classified.Kind != test.wantKind || classified.Code != test.wantCode {
				t.Fatalf("classified error = %#v", classified)
			}
			if classified.RetryAfter == nil || classified.RetryAfter.DelaySeconds == nil || *classified.RetryAfter.DelaySeconds != 17 {
				t.Fatalf("retry-after = %#v", classified.RetryAfter)
			}
		})
	}
}

func TestGeminiUsesStructuredQuotaAndRetryFacts(t *testing.T) {
	t.Parallel()

	classified := NewGemini().ClassifyError(http.StatusTooManyRequests, nil, []byte(`{
		"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota exhausted","details":[
			{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"}]},
			{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"4.250s"}
		]}
	}`))
	if classified.Kind != canonical.ErrorRateLimit || classified.RetryAfter == nil || classified.RetryAfter.DelaySeconds == nil || *classified.RetryAfter.DelaySeconds != 5 {
		t.Fatalf("classified error = %#v", classified)
	}

	daily := NewGemini().ClassifyError(http.StatusTooManyRequests, nil, []byte(`{
		"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota exhausted","details":[
			{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier"}]}
		]}
	}`))
	if daily.Kind != canonical.ErrorQuota {
		t.Fatalf("daily quota error = %#v", daily)
	}

	unavailable := NewGemini().ClassifyError(http.StatusServiceUnavailable, nil, []byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"overloaded"}}`))
	if unavailable.Kind != canonical.ErrorProviderTemporary || !unavailable.ReplaySafe {
		t.Fatalf("unavailable error = %#v", unavailable)
	}
}
