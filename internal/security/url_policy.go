package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrInvalidSSRFPolicy = errors.New("invalid SSRF policy")
	ErrUnsafeURL         = errors.New("outbound URL is not allowed")
	ErrURLResolution     = errors.New("outbound URL resolution failed")
)

type URLPolicyErrorKind string

const (
	URLMalformedError      URLPolicyErrorKind = "malformed_url"
	URLSchemeError         URLPolicyErrorKind = "scheme"
	URLUserInfoError       URLPolicyErrorKind = "userinfo"
	URLHostError           URLPolicyErrorKind = "host"
	URLBlockedAddressError URLPolicyErrorKind = "blocked_address"
	URLResolutionError     URLPolicyErrorKind = "resolution"
	URLRedirectError       URLPolicyErrorKind = "redirect"
)

type URLPolicyError struct {
	Kind  URLPolicyErrorKind
	Host  string
	Cause error
}

func (e *URLPolicyError) Error() string {
	if e.Host == "" {
		return fmt.Sprintf("outbound URL rejected: %s", e.Kind)
	}
	return fmt.Sprintf("outbound URL host %q rejected: %s", e.Host, e.Kind)
}

func (e *URLPolicyError) Unwrap() error {
	return e.Cause
}

func (e *URLPolicyError) Is(target error) bool {
	if target == ErrURLResolution {
		return e.Kind == URLResolutionError
	}
	return target == ErrUnsafeURL && e.Kind != URLResolutionError
}

// SSRFPolicy permits only explicitly listed private networks. Loopback,
// link-local, multicast and known metadata endpoints remain blocked.
type SSRFPolicy struct {
	AllowedPrivatePrefixes  []netip.Prefix
	AllowedResolvedPrefixes []netip.Prefix
	MaxRedirects            int
}

type netIPResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type URLValidator struct {
	allowedPrivatePrefixes  []netip.Prefix
	allowedResolvedPrefixes []netip.Prefix
	resolver                netIPResolver
}

func NewURLValidator(policy SSRFPolicy) (*URLValidator, error) {
	prefixes, err := validateAllowedPrivatePrefixes(policy.AllowedPrivatePrefixes)
	if err != nil {
		return nil, err
	}
	resolvedPrefixes, err := validateAllowedResolvedPrefixes(policy.AllowedResolvedPrefixes)
	if err != nil {
		return nil, err
	}
	return &URLValidator{
		allowedPrivatePrefixes:  prefixes,
		allowedResolvedPrefixes: resolvedPrefixes,
		resolver:                net.DefaultResolver,
	}, nil
}

func (v *URLValidator) ValidateString(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, &URLPolicyError{Kind: URLMalformedError, Cause: err}
	}
	if err := v.ValidateURL(ctx, parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (v *URLValidator) ValidateURL(ctx context.Context, candidate *url.URL) error {
	host, err := validateURLStructure(candidate)
	if err != nil {
		return err
	}
	_, err = v.resolveAndValidate(ctx, host)
	return err
}

func (v *URLValidator) resolveAndValidate(ctx context.Context, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		literal = literal.Unmap()
		if err := v.validateAddress(literal); err != nil {
			return nil, err
		}
		return []netip.Addr{literal}, nil
	}

	addresses, err := v.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, &URLPolicyError{Kind: URLResolutionError, Host: host, Cause: err}
	}
	if len(addresses) == 0 {
		return nil, &URLPolicyError{Kind: URLResolutionError, Host: host, Cause: errors.New("no IP addresses returned")}
	}

	validated := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if err := v.validateAddress(address); err != nil {
			return nil, err
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		validated = append(validated, address)
	}
	return validated, nil
}

func (v *URLValidator) validateAddress(address netip.Addr) error {
	if !address.IsValid() || address.Zone() != "" || isMetadataAddress(address) ||
		address.IsUnspecified() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsMulticast() {
		return &URLPolicyError{Kind: URLBlockedAddressError, Host: address.String()}
	}
	for _, prefix := range v.allowedResolvedPrefixes {
		if prefix.Contains(address) {
			return nil
		}
	}
	if address.IsPrivate() {
		for _, prefix := range v.allowedPrivatePrefixes {
			if prefix.Contains(address) {
				return nil
			}
		}
		return &URLPolicyError{Kind: URLBlockedAddressError, Host: address.String()}
	}
	if !address.IsGlobalUnicast() || isReservedAddress(address) {
		return &URLPolicyError{Kind: URLBlockedAddressError, Host: address.String()}
	}
	return nil
}

func validateURLStructure(candidate *url.URL) (string, error) {
	if candidate == nil || candidate.Opaque != "" || candidate.Host == "" {
		return "", &URLPolicyError{Kind: URLMalformedError}
	}
	if !strings.EqualFold(candidate.Scheme, "http") && !strings.EqualFold(candidate.Scheme, "https") {
		return "", &URLPolicyError{Kind: URLSchemeError}
	}
	if candidate.User != nil {
		return "", &URLPolicyError{Kind: URLUserInfoError}
	}
	host := strings.TrimSuffix(strings.ToLower(candidate.Hostname()), ".")
	if host == "" || strings.Contains(host, "%") {
		return "", &URLPolicyError{Kind: URLHostError}
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || isMetadataHostname(host) {
		return "", &URLPolicyError{Kind: URLBlockedAddressError, Host: host}
	}
	if port := candidate.Port(); port != "" {
		portNumber, err := strconv.ParseUint(port, 10, 16)
		if err != nil || portNumber == 0 {
			return "", &URLPolicyError{Kind: URLHostError, Host: host, Cause: err}
		}
	}
	return host, nil
}

func validateAllowedPrivatePrefixes(prefixes []netip.Prefix) ([]netip.Prefix, error) {
	validated := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		if !prefix.IsValid() || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("%w: invalid private prefix", ErrInvalidSSRFPolicy)
		}
		prefix = prefix.Masked()
		address := prefix.Addr()
		if !address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
			return nil, fmt.Errorf("%w: allowed prefix must be a private network", ErrInvalidSSRFPolicy)
		}
		validated = append(validated, prefix)
	}
	return validated, nil
}

func validateAllowedResolvedPrefixes(prefixes []netip.Prefix) ([]netip.Prefix, error) {
	validated := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		if !prefix.IsValid() || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("%w: invalid resolved prefix", ErrInvalidSSRFPolicy)
		}
		prefix = prefix.Masked()
		if prefix.Addr().Is4() && prefix.Bits() < 8 || prefix.Addr().Is6() && prefix.Bits() < 32 {
			return nil, fmt.Errorf("%w: resolved prefix is too broad", ErrInvalidSSRFPolicy)
		}
		validated = append(validated, prefix)
	}
	return validated, nil
}

var metadataAddresses = map[netip.Addr]struct{}{
	netip.MustParseAddr("169.254.169.254"): {},
	netip.MustParseAddr("100.100.100.200"): {},
	netip.MustParseAddr("168.63.129.16"):   {},
	netip.MustParseAddr("fd00:ec2::254"):   {},
}

var reservedAddressPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fec0::/10"),
}

func isMetadataAddress(address netip.Addr) bool {
	_, ok := metadataAddresses[address]
	return ok
}

func isReservedAddress(address netip.Addr) bool {
	for _, prefix := range reservedAddressPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func isMetadataHostname(host string) bool {
	switch host {
	case "metadata.google.internal", "metadata.azure.internal", "instance-data":
		return true
	default:
		return false
	}
}
