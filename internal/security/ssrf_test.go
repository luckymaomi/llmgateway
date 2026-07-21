package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"testing"
)

type staticResolver map[string][]netip.Addr

func (r staticResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	addresses, ok := r[host]
	if !ok {
		return nil, errors.New("host not found")
	}
	return addresses, nil
}

type sequenceResolver struct {
	mu        sync.Mutex
	responses [][]netip.Addr
	calls     int
}

func (r *sequenceResolver) LookupNetIP(_ context.Context, _ string, _ string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.calls
	if index >= len(r.responses) {
		index = len(r.responses) - 1
	}
	r.calls++
	return r.responses[index], nil
}

type recordingDialer struct {
	mu        sync.Mutex
	addresses []string
	err       error
}

func (d *recordingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addresses = append(d.addresses, address)
	return nil, d.err
}

func TestURLValidatorAcceptsPublicHTTPAddress(t *testing.T) {
	validator, err := NewURLValidator(SSRFPolicy{})
	if err != nil {
		t.Fatalf("NewURLValidator() error = %v", err)
	}
	validator.resolver = staticResolver{
		"api.example.com": {netip.MustParseAddr("93.184.216.34")},
	}

	parsed, err := validator.ValidateString(context.Background(), "https://api.example.com/v1/models")
	if err != nil {
		t.Fatalf("ValidateString() error = %v", err)
	}
	if parsed.Hostname() != "api.example.com" {
		t.Fatalf("ValidateString() host = %q", parsed.Hostname())
	}
}

func TestURLValidatorBlocksUnsafeDestinations(t *testing.T) {
	validator, err := NewURLValidator(SSRFPolicy{})
	if err != nil {
		t.Fatalf("NewURLValidator() error = %v", err)
	}
	validator.resolver = staticResolver{
		"private.example": {netip.MustParseAddr("10.20.30.40")},
		"mixed.example": {
			netip.MustParseAddr("93.184.216.34"),
			netip.MustParseAddr("10.20.30.40"),
		},
	}

	for _, rawURL := range []string{
		"file:///etc/passwd",
		"https://user:password@private.example/path",
		"http://localhost/status",
		"http://127.0.0.1/status",
		"http://169.254.169.254/latest/meta-data",
		"http://168.63.129.16/metadata",
		"https://private.example/v1",
		"https://mixed.example/v1",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := validator.ValidateString(context.Background(), rawURL); !errors.Is(err, ErrUnsafeURL) {
				t.Fatalf("ValidateString(%q) error = %v, want ErrUnsafeURL", rawURL, err)
			}
		})
	}
}

func TestURLValidatorAllowsLiteralLoopbackOnlyWhenExplicitlyEnabled(t *testing.T) {
	validator, err := NewURLValidator(SSRFPolicy{AllowLoopback: true})
	if err != nil {
		t.Fatalf("NewURLValidator() error = %v", err)
	}
	if _, err := validator.ValidateString(context.Background(), "https://127.0.0.1:8443/v1"); err != nil {
		t.Fatalf("explicit loopback address rejected: %v", err)
	}
	if _, err := validator.ValidateString(context.Background(), "https://localhost:8443/v1"); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("localhost error = %v, want ErrUnsafeURL", err)
	}
}

func TestURLValidatorAllowsOnlyConfiguredPrivateNetwork(t *testing.T) {
	validator, err := NewURLValidator(SSRFPolicy{
		AllowedPrivatePrefixes: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")},
	})
	if err != nil {
		t.Fatalf("NewURLValidator() error = %v", err)
	}
	validator.resolver = staticResolver{
		"controlled.internal": {netip.MustParseAddr("10.20.30.40")},
		"other.internal":      {netip.MustParseAddr("10.21.30.40")},
	}
	if _, err := validator.ValidateString(context.Background(), "http://controlled.internal/service"); err != nil {
		t.Fatalf("configured private address rejected: %v", err)
	}
	if _, err := validator.ValidateString(context.Background(), "http://other.internal/service"); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("unconfigured private address error = %v, want ErrUnsafeURL", err)
	}
}

func TestURLValidatorSupportsExplicitFakeIPNetwork(t *testing.T) {
	validator, err := NewURLValidator(SSRFPolicy{
		AllowedResolvedPrefixes: []netip.Prefix{netip.MustParsePrefix("198.18.0.0/15")},
	})
	if err != nil {
		t.Fatalf("NewURLValidator() error = %v", err)
	}
	validator.resolver = staticResolver{
		"api.provider.example": {netip.MustParseAddr("198.18.2.88")},
	}
	if _, err := validator.ValidateString(context.Background(), "https://api.provider.example/v1"); err != nil {
		t.Fatalf("configured Fake-IP address rejected: %v", err)
	}
}

func TestURLValidatorClassifiesResolverFailureSeparately(t *testing.T) {
	validator, err := NewURLValidator(SSRFPolicy{})
	if err != nil {
		t.Fatalf("NewURLValidator() error = %v", err)
	}
	validator.resolver = staticResolver{}

	_, err = validator.ValidateString(context.Background(), "https://unavailable.example/v1")
	if !errors.Is(err, ErrURLResolution) {
		t.Fatalf("ValidateString() error = %v, want ErrURLResolution", err)
	}
	if errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("resolver failure must not be classified as policy rejection: %v", err)
	}
}

func TestSSRFSafeTransportRechecksDNSBeforeDial(t *testing.T) {
	transport, err := NewSSRFSafeTransport(SSRFPolicy{})
	if err != nil {
		t.Fatalf("NewSSRFSafeTransport() error = %v", err)
	}
	resolver := &sequenceResolver{responses: [][]netip.Addr{
		{netip.MustParseAddr("93.184.216.34")},
		{netip.MustParseAddr("10.20.30.40")},
	}}
	dialer := &recordingDialer{err: errors.New("dial must not run")}
	transport.validator.resolver = resolver
	transport.dialer = dialer

	request, err := http.NewRequest(http.MethodGet, "http://rebind.example/data", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	_, err = transport.RoundTrip(request)
	if !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("RoundTrip() error = %v, want ErrUnsafeURL", err)
	}
	if len(dialer.addresses) != 0 {
		t.Fatalf("dialer addresses = %v, want no dial", dialer.addresses)
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls = %d, want request and dial rechecks", resolver.calls)
	}
}

func TestSSRFSafeTransportDialsValidatedIPAddress(t *testing.T) {
	transport, err := NewSSRFSafeTransport(SSRFPolicy{})
	if err != nil {
		t.Fatalf("NewSSRFSafeTransport() error = %v", err)
	}
	transport.validator.resolver = staticResolver{
		"public.example": {netip.MustParseAddr("93.184.216.34")},
	}
	dialFailure := errors.New("expected test dial failure")
	dialer := &recordingDialer{err: dialFailure}
	transport.dialer = dialer

	request, err := http.NewRequest(http.MethodGet, "http://public.example/data", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	_, err = transport.RoundTrip(request)
	if !errors.Is(err, dialFailure) {
		t.Fatalf("RoundTrip() error = %v, want dial failure", err)
	}
	if len(dialer.addresses) != 1 || dialer.addresses[0] != "93.184.216.34:80" {
		t.Fatalf("dialer addresses = %v, want validated IP", dialer.addresses)
	}
}

func TestSSRFSafeClientRevalidatesRedirects(t *testing.T) {
	client, err := NewSSRFSafeClient(SSRFPolicy{MaxRedirects: 1})
	if err != nil {
		t.Fatalf("NewSSRFSafeClient() error = %v", err)
	}
	transport := client.Transport.(*SSRFSafeTransport)
	transport.validator.resolver = staticResolver{
		"private.example": {netip.MustParseAddr("10.20.30.40")},
		"public.example":  {netip.MustParseAddr("93.184.216.34")},
	}

	privateRedirect := &http.Request{URL: &url.URL{Scheme: "https", Host: "private.example"}}
	via := []*http.Request{{URL: &url.URL{Scheme: "https", Host: "public.example"}}}
	if err := client.CheckRedirect(privateRedirect, via); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("CheckRedirect(private) error = %v, want ErrUnsafeURL", err)
	}

	publicRedirect := &http.Request{URL: &url.URL{Scheme: "https", Host: "public.example"}}
	via = append(via, publicRedirect)
	var policyError *URLPolicyError
	if err := client.CheckRedirect(publicRedirect, via); !errors.As(err, &policyError) || policyError.Kind != URLRedirectError {
		t.Fatalf("CheckRedirect(limit) error = %v", err)
	}
}
