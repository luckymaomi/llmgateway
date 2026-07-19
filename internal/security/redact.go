package security

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const RedactedValue = "[REDACTED]"

// RedactHeaders returns a detached copy suitable for logs and diagnostics.
func RedactHeaders(headers http.Header) http.Header {
	redacted := headers.Clone()
	for name := range redacted {
		if isSensitiveLogKey(name) {
			redacted[name] = []string{RedactedValue}
		}
	}
	return redacted
}

// RedactURL removes user information and sensitive query or fragment values.
// Malformed input is never echoed because it may itself contain a credential.
func RedactURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return RedactedValue
	}
	parsed.User = nil
	query := parsed.Query()
	for name := range query {
		if isSensitiveLogKey(name) {
			query[name] = []string{RedactedValue}
		}
	}
	parsed.RawQuery = query.Encode()
	if parsed.Fragment != "" {
		parsed.Fragment = RedactedValue
	}
	return parsed.String()
}

// RedactingHandler enforces field-aware redaction before records reach the
// configured slog destination. Message text must still contain no secrets.
type RedactingHandler struct {
	next           slog.Handler
	sensitiveGroup bool
}

func NewRedactingHandler(next slog.Handler) *RedactingHandler {
	if next == nil {
		panic("security.NewRedactingHandler: nil handler")
	}
	return &RedactingHandler{next: next}
}

func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *RedactingHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		redacted.AddAttrs(redactLogAttribute(attribute, h.sensitiveGroup))
		return true
	})
	return h.next.Handle(ctx, redacted)
}

func (h *RedactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attributes))
	for index, attribute := range attributes {
		redacted[index] = redactLogAttribute(attribute, h.sensitiveGroup)
	}
	return &RedactingHandler{
		next:           h.next.WithAttrs(redacted),
		sensitiveGroup: h.sensitiveGroup,
	}
}

func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{
		next:           h.next.WithGroup(name),
		sensitiveGroup: h.sensitiveGroup || isSensitiveLogKey(name),
	}
}

func redactLogAttribute(attribute slog.Attr, sensitiveGroup bool) slog.Attr {
	if sensitiveGroup || isSensitiveLogKey(attribute.Key) {
		return slog.String(attribute.Key, RedactedValue)
	}

	value := attribute.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		group := value.Group()
		redacted := make([]slog.Attr, len(group))
		for index, child := range group {
			redacted[index] = redactLogAttribute(child, false)
		}
		return slog.Group(attribute.Key, attrsToAny(redacted)...)
	}
	if isURLLogKey(attribute.Key) && value.Kind() == slog.KindString {
		return slog.String(attribute.Key, RedactURL(value.String()))
	}
	if value.Kind() == slog.KindAny {
		switch typed := value.Any().(type) {
		case http.Header:
			return slog.Any(attribute.Key, RedactHeaders(typed))
		case url.URL:
			return slog.String(attribute.Key, RedactURL(typed.String()))
		case *url.URL:
			if typed == nil {
				return slog.Any(attribute.Key, nil)
			}
			return slog.String(attribute.Key, RedactURL(typed.String()))
		}
	}
	attribute.Value = value
	return attribute
}

func attrsToAny(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index := range attributes {
		values[index] = attributes[index]
	}
	return values
}

func isSensitiveLogKey(name string) bool {
	normalized := normalizeLogKey(name)
	switch normalized {
	case "authorization", "proxy_authorization", "cookie", "set_cookie",
		"api_key", "apikey", "gateway_key", "key", "password", "passwd",
		"secret", "client_secret", "credential", "credentials",
		"token", "access_token", "refresh_token", "session_token", "private_key",
		"signature", "sig",
		"master_key", "session_pepper", "api_key_pepper":
		return true
	}
	for _, suffix := range []string{
		"_api_key", "_password", "_secret", "_credential", "_token",
		"_private_key", "_pepper",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	compact := strings.ReplaceAll(normalized, "_", "")
	for _, suffix := range []string{
		"apikey", "password", "secret", "credential", "accesstoken",
		"refreshtoken", "sessiontoken", "privatekey", "pepper",
	} {
		if strings.HasSuffix(compact, suffix) {
			return true
		}
	}
	return false
}

func isURLLogKey(name string) bool {
	normalized := normalizeLogKey(name)
	return normalized == "url" || strings.HasSuffix(normalized, "_url")
}

func normalizeLogKey(name string) string {
	var normalized strings.Builder
	lastSeparator := false
	for _, character := range strings.ToLower(name) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
			lastSeparator = false
			continue
		}
		if normalized.Len() > 0 && !lastSeparator {
			normalized.WriteByte('_')
			lastSeparator = true
		}
	}
	return strings.TrimSuffix(normalized.String(), "_")
}
