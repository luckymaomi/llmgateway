package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

type contextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type SSRFSafeTransport struct {
	base      *http.Transport
	validator *URLValidator
	dialer    contextDialer
}

func NewSSRFSafeTransport(policy SSRFPolicy) (*SSRFSafeTransport, error) {
	validator, err := NewURLValidator(policy)
	if err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: default HTTP transport is unavailable", ErrInvalidSSRFPolicy)
	}

	transport := &SSRFSafeTransport{
		base:      base.Clone(),
		validator: validator,
		dialer:    &net.Dialer{},
	}
	// Environment proxy variables must not silently create a second outbound
	// policy. A configured proxy is represented as an explicitly validated
	// upstream in the owning Provider policy.
	transport.base.Proxy = nil
	transport.base.DialContext = transport.dialContext
	return transport, nil
}

func (t *SSRFSafeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, &URLPolicyError{Kind: URLMalformedError}
	}
	// This lookup is intentional even when a pooled connection exists. It
	// prevents a rebinding result from becoming trusted merely because an older
	// connection to the same hostname is reusable.
	if err := t.validator.ValidateURL(request.Context(), request.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

func (t *SSRFSafeTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
}

func (t *SSRFSafeTransport) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, &URLPolicyError{Kind: URLHostError, Cause: err}
	}
	host = strings.TrimSuffix(host, ".")
	addresses, err := t.validator.resolveAndValidate(ctx, host)
	if err != nil {
		return nil, err
	}

	var dialErrors []error
	for _, validatedAddress := range addresses {
		if (network == "tcp4" && !validatedAddress.Is4()) || (network == "tcp6" && !validatedAddress.Is6()) {
			continue
		}
		connection, dialErr := t.dialer.DialContext(ctx, network, net.JoinHostPort(validatedAddress.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		dialErrors = append(dialErrors, dialErr)
	}
	if len(dialErrors) == 0 {
		return nil, &URLPolicyError{Kind: URLResolutionError, Host: host, Cause: errors.New("no address matches requested network")}
	}
	return nil, fmt.Errorf("connect to validated upstream: %w", errors.Join(dialErrors...))
}

func NewSSRFSafeClient(policy SSRFPolicy) (*http.Client, error) {
	if policy.MaxRedirects < 0 || policy.MaxRedirects > 20 {
		return nil, fmt.Errorf("%w: max redirects must be between 0 and 20", ErrInvalidSSRFPolicy)
	}
	transport, err := NewSSRFSafeTransport(policy)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > policy.MaxRedirects {
			return &URLPolicyError{Kind: URLRedirectError, Host: request.URL.Hostname()}
		}
		return transport.validator.ValidateURL(request.Context(), request.URL)
	}
	return client, nil
}
