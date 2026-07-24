package credentialprobe

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/luckymaomi/llmgateway/internal/security"
)

func TestTransportFailuresProduceActionableProbeResults(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    string
		errorKind string
		retryable bool
	}{
		{name: "timeout", err: context.DeadlineExceeded, status: "uncertain", errorKind: "probe_timeout_or_canceled"},
		{name: "dns", err: fmt.Errorf("resolve: %w", security.ErrURLResolution), status: "failed", errorKind: "dns_resolution_failed", retryable: true},
		{name: "outbound policy", err: fmt.Errorf("validate: %w", security.ErrUnsafeURL), status: "failed", errorKind: "outbound_address_blocked"},
		{name: "tls", err: x509.UnknownAuthorityError{}, status: "failed", errorKind: "tls_handshake_failed"},
		{name: "connection", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, status: "failed", errorKind: "upstream_connection_failed", retryable: true},
		{name: "transport", err: errors.New("transport failed"), status: "failed", errorKind: "provider_transport_failed", retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, errorKind, retryable := classifyTransportFailure(test.err)
			if status != test.status || errorKind == nil || *errorKind != test.errorKind || retryable != test.retryable {
				t.Fatalf("classifyTransportFailure() = (%q, %v, %t), want (%q, %q, %t)", status, errorKind, retryable, test.status, test.errorKind, test.retryable)
			}
		})
	}
}
