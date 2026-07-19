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
	Message   string     `json:"message"`
	Type      string     `json:"type"`
	Parameter string     `json:"param"`
	Code      stringCode `json:"code"`
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
			Kind: a.policy.classify(statusCode, ""), Code: strconv.Itoa(statusCode),
			Message: "provider request failed", Provider: string(a.policy.kind), HTTPStatus: statusCode,
			RetryAfter: parseRetryAfter(headers),
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
	kind := a.policy.classify(statusCode, code)
	message := strings.TrimSpace(providerError.Message)
	if message == "" {
		message = "provider request failed"
	}
	if requestID == "" && a.policy.responseRequestIDHeader != "" {
		requestID = headers.Get(a.policy.responseRequestIDHeader)
	}
	return &canonical.Error{
		Kind: kind, Code: code, Message: message, Parameter: providerError.Parameter,
		Provider: string(a.policy.kind), ProviderType: providerError.Type, RequestID: requestID, HTTPStatus: statusCode,
		RetryAfter: parseRetryAfter(headers),
	}
}

func classifyHTTPError(statusCode int, _ string) canonical.ErrorKind {
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

func classifyDeepSeekError(statusCode int, _ string) canonical.ErrorKind {
	return classifyHTTPError(statusCode, "")
}

func classifyZhipuError(statusCode int, code string) canonical.ErrorKind {
	switch code {
	case "1000", "1001", "1002", "1003", "1004", "1110", "1111", "1112":
		return canonical.ErrorAuthentication
	case "1113", "1304", "1308", "1309", "1310":
		return canonical.ErrorQuota
	case "1210", "1213", "1214", "1215", "1261":
		return canonical.ErrorInvalidRequest
	case "1211", "1221", "1222":
		return canonical.ErrorProviderConfiguration
	case "1212":
		return canonical.ErrorUnsupportedCapability
	case "1220", "1301", "1311":
		return canonical.ErrorPermission
	case "1302":
		return canonical.ErrorRateLimit
	case "500", "1120", "1230", "1234", "1305":
		return canonical.ErrorProviderTemporary
	case "1121", "1231", "1300":
		return canonical.ErrorProviderPermanent
	default:
		return classifyHTTPError(statusCode, code)
	}
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
