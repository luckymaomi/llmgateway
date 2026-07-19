package providers

import (
	"testing"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func TestZhipuStreamFinishKindsDriveCanonicalBehavior(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		finish     string
		role       string
		wantType   canonical.StreamEventType
		wantFinish canonical.FinishReason
		wantError  canonical.ErrorKind
	}{
		{
			name: "content safety finish", finish: "sensitive", role: "user",
			wantType: canonical.StreamFinish, wantFinish: canonical.FinishReasonContentFilter,
		},
		{
			name: "provider network finish", finish: "network_error",
			wantType: canonical.StreamError, wantError: canonical.ErrorProviderTemporary,
		},
		{
			name: "context window finish", finish: "model_context_window_exceeded",
			wantType: canonical.StreamError, wantError: canonical.ErrorInvalidRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			roleField := ""
			if test.role != "" {
				roleField = `"role":"` + test.role + `",`
			}
			input := `data: {"id":"glm-finish-1","request_id":"zhipu-request-2","created":1710000103,"model":"glm-5.2","choices":[{"index":0,"delta":{` + roleField + `"content":""},"finish_reason":"` + test.finish + `"}]}` + "\n\n"
			events, err := NewZhipu().ParseStream().Feed([]byte(input))
			if err != nil {
				t.Fatalf("parse stream finish: %v", err)
			}
			last := events[len(events)-1]
			if last.Type != test.wantType {
				t.Fatalf("last event = %#v", last)
			}
			if test.wantFinish != "" && last.FinishReason != test.wantFinish {
				t.Fatalf("finish reason = %q", last.FinishReason)
			}
			if test.wantError != "" && (last.Error == nil || last.Error.Kind != test.wantError) {
				t.Fatalf("error event = %#v", last)
			}
		})
	}
}
