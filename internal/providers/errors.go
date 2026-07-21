package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

type wireErrorEnvelope struct {
	Error     *wireError `json:"error"`
	Message   string     `json:"message"`
	Type      string     `json:"type"`
	Parameter string     `json:"param"`
	Code      stringCode `json:"code"`
	RequestID string     `json:"request_id"`
}

type wireError struct {
	Message   string            `json:"message"`
	Type      string            `json:"type"`
	Status    string            `json:"status"`
	Parameter string            `json:"param"`
	Code      stringCode        `json:"code"`
	Details   []wireErrorDetail `json:"details"`
}

type wireErrorDetail struct {
	Type       string               `json:"@type"`
	RetryDelay json.RawMessage      `json:"retryDelay"`
	Violations []wireQuotaViolation `json:"violations"`
}

type wireQuotaViolation struct {
	QuotaID string `json:"quotaId"`
}

type stringCode string

func (code *stringCode) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*code = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*code = stringCode(text)
		return nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("error code must be one JSON scalar")
	}
	*code = stringCode(number.String())
	return nil
}

func (a *openAIAdapter) ClassifyError(statusCode int, headers http.Header, body []byte) *canonical.Error {
	var envelope wireErrorEnvelope
	if err := decodeJSON(body, &envelope); err != nil {
		return &canonical.Error{
			Kind: a.policy.classify(statusCode, nil), Code: strconv.Itoa(statusCode),
			Message: "provider request failed", Provider: string(a.policy.kind), HTTPStatus: statusCode,
			RetryAfter: a.policy.retryAfter(headers, nil),
		}
	}
	providerError := envelope.Error
	if providerError == nil {
		providerError = &wireError{Message: envelope.Message, Type: envelope.Type, Parameter: envelope.Parameter, Code: envelope.Code}
	}
	return a.classifyWireError(statusCode, headers, providerError, envelope.RequestID)
}

func (a *openAIAdapter) classifyWireError(statusCode int, headers http.Header, providerError *wireError, requestID string) *canonical.Error {
	code := string(providerError.Code)
	kind := a.policy.classify(statusCode, providerError)
	message := strings.TrimSpace(providerError.Message)
	if message == "" {
		message = "provider request failed"
	}
	if requestID == "" && a.policy.responseRequestIDHeader != "" {
		requestID = headers.Get(a.policy.responseRequestIDHeader)
	}
	providerType := providerError.Type
	if providerType == "" {
		providerType = providerError.Status
	}
	return &canonical.Error{
		Kind: kind, Code: code, Message: message, Parameter: providerError.Parameter,
		Provider: string(a.policy.kind), ProviderType: providerType, RequestID: requestID, HTTPStatus: statusCode,
		RetryAfter: a.policy.retryAfter(headers, providerError), ReplaySafe: a.policy.replaySafe != nil && a.policy.replaySafe(statusCode, providerError),
	}
}

func classifyHTTPError(statusCode int, _ *wireError) canonical.ErrorKind {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return canonical.ErrorInvalidRequest
	case http.StatusUnauthorized:
		return canonical.ErrorAuthentication
	case http.StatusPaymentRequired:
		return canonical.ErrorQuota
	case http.StatusForbidden:
		return canonical.ErrorPermission
	case http.StatusNotFound:
		return canonical.ErrorProviderConfiguration
	case http.StatusRequestTimeout:
		return canonical.ErrorProviderTemporary
	case http.StatusTooManyRequests:
		return canonical.ErrorRateLimit
	default:
		if statusCode >= http.StatusInternalServerError {
			return canonical.ErrorProviderTemporary
		}
		return canonical.ErrorProviderPermanent
	}
}

func standardRetryAfter(headers http.Header, _ *wireError) *canonical.RetryAfter {
	return parseRetryAfter(headers)
}

func parseRetryAfter(headers http.Header) *canonical.RetryAfter {
	if headers == nil {
		return nil
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return nil
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		return &canonical.RetryAfter{DelaySeconds: &seconds}
	}
	if at, err := http.ParseTime(raw); err == nil {
		at = at.UTC()
		return &canonical.RetryAfter{At: &at}
	}
	return nil
}
