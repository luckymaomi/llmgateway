package security

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"testing"
)

func TestRedactionHelpersPreserveOperationalFields(t *testing.T) {
	headers := http.Header{
		"Authorization": {"Bearer upstream-secret"},
		"X-Api-Key":     {"provider-secret"},
		"X-Auth-Token":  {"session-secret"},
		"X-Request-Id":  {"request-17"},
	}
	redactedHeaders := RedactHeaders(headers)
	if redactedHeaders.Get("Authorization") != RedactedValue || redactedHeaders.Get("X-Api-Key") != RedactedValue || redactedHeaders.Get("X-Auth-Token") != RedactedValue {
		t.Fatalf("RedactHeaders() = %#v", redactedHeaders)
	}
	if redactedHeaders.Get("X-Request-Id") != "request-17" {
		t.Fatalf("request ID = %q", redactedHeaders.Get("X-Request-Id"))
	}
	if headers.Get("Authorization") != "Bearer upstream-secret" {
		t.Fatal("RedactHeaders() mutated its input")
	}

	redactedURL := RedactURL("https://user:password@api.example.com/v1?api_key=secret&region=cn#private")
	parsed, err := url.Parse(redactedURL)
	if err != nil {
		t.Fatalf("url.Parse(redacted) error = %v", err)
	}
	if parsed.User != nil || parsed.Query().Get("api_key") != RedactedValue || parsed.Query().Get("region") != "cn" || parsed.Fragment != RedactedValue {
		t.Fatalf("RedactURL() = %q", redactedURL)
	}
}

func TestRedactingHandlerEmitsStructuredSafeValues(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil)))
	logger.Info(
		"upstream request",
		slog.String("providerAPIKey", "provider-secret"),
		slog.Int("token_count", 128),
		slog.String("request_url", "https://api.example.com/v1?access_token=secret&page=2"),
		slog.Group("result",
			slog.String("password", "user-secret"),
			slog.String("state", "ready"),
		),
	)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal(log) error = %v; output = %s", err, output.String())
	}
	if record["providerAPIKey"] != RedactedValue || record["token_count"] != float64(128) {
		t.Fatalf("redacted record = %#v", record)
	}
	loggedURL, err := url.Parse(record["request_url"].(string))
	if err != nil || loggedURL.Query().Get("access_token") != RedactedValue || loggedURL.Query().Get("page") != "2" {
		t.Fatalf("logged URL = %v, %v", record["request_url"], err)
	}
	result := record["result"].(map[string]any)
	if result["password"] != RedactedValue || result["state"] != "ready" {
		t.Fatalf("nested result = %#v", result)
	}
}

func TestRedactingHandlerProtectsAttachedAndGroupedAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewRedactingHandler(slog.NewJSONHandler(&output, nil)))
	logger = logger.With(slog.String("upstreamCredential", "provider-secret")).WithGroup("credentials")
	logger.Info("credential state", slog.String("value", "session-secret"), slog.String("state", "ready"))

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal(log) error = %v; output = %s", err, output.String())
	}
	if record["upstreamCredential"] != RedactedValue {
		t.Fatalf("attached credential = %#v", record["upstreamCredential"])
	}
	credentials := record["credentials"].(map[string]any)
	if credentials["value"] != RedactedValue || credentials["state"] != RedactedValue {
		t.Fatalf("credential group = %#v", credentials)
	}
}
