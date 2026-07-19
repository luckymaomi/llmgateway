package providers

import (
	"context"
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

type Kind string

const (
	KindOpenAICompatible Kind = "openai-compatible"
	KindDeepSeek         Kind = "deepseek"
	KindZhipu            Kind = "zhipu"
	KindAgnes            Kind = "agnes"
)

type Credential struct {
	APIKey string
}

type ProbeKind string

const (
	ProbeModels ProbeKind = "models"
)

type Probe struct {
	Available        bool
	MayConsumeTokens bool
	Kind             ProbeKind
	Request          *http.Request
}

type StreamParser interface {
	Feed([]byte) ([]canonical.StreamEvent, error)
	Close() ([]canonical.StreamEvent, error)
}

type Adapter interface {
	Kind() Kind
	Capabilities() Capabilities
	BuildRequest(context.Context, Credential, canonical.ChatRequest) (*http.Request, error)
	ParseResponse(statusCode int, headers http.Header, body []byte) (canonical.ChatResponse, error)
	ParseStream() StreamParser
	ClassifyError(statusCode int, headers http.Header, body []byte) *canonical.Error
	Probe(context.Context, Credential) (Probe, error)
}
