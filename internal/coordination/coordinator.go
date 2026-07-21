package coordination

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	minimumHashKeyBytes = 32
	maximumSubjectBytes = 512
)

var prefixPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,79}$`)

type Options struct {
	Prefix        string
	KeyHashSecret []byte
}

type Coordinator struct {
	client        redis.Scripter
	prefix        string
	keyHashSecret []byte
}

// New creates a fail-closed Valkey coordinator. KeyHashSecret provides domain
// separation for all caller identifiers written to Valkey.
func New(client redis.Scripter, options Options) (*Coordinator, error) {
	if client == nil {
		return nil, fmt.Errorf("%w: Valkey client is required", ErrInvalidInput)
	}
	if !prefixPattern.MatchString(options.Prefix) {
		return nil, fmt.Errorf("%w: prefix must contain 1-80 safe characters", ErrInvalidInput)
	}
	if len(options.KeyHashSecret) < minimumHashKeyBytes {
		return nil, fmt.Errorf("%w: key hash secret must contain at least %d bytes", ErrInvalidInput, minimumHashKeyBytes)
	}
	secret := append([]byte(nil), options.KeyHashSecret...)
	return &Coordinator{client: client, prefix: options.Prefix, keyHashSecret: secret}, nil
}

// NewLeaseID creates a high-entropy ID suitable for LeaseRef.ID.
func NewLeaseID() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate coordination lease ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func (c *Coordinator) rateKey(limit BucketLimit) string {
	return c.prefix + ":{coordination}:rate:" + string(limit.Metric) + ":" + string(limit.Dimension.Scope) + ":" + c.digest("dimension", string(limit.Dimension.Scope), limit.Dimension.SubjectID)
}

func (c *Coordinator) leaseKey(dimension Dimension) string {
	return c.prefix + ":{coordination}:lease:" + string(dimension.Scope) + ":" + c.digest("dimension", string(dimension.Scope), dimension.SubjectID)
}

func (c *Coordinator) leaseMember(leaseID string) string {
	return c.digest("lease", leaseID)
}

func (c *Coordinator) digest(parts ...string) string {
	mac := hmac.New(sha256.New, c.keyHashSecret)
	var length [4]byte
	for _, part := range parts {
		binary.BigEndian.PutUint32(length[:], uint32(len(part)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write([]byte(part))
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func validateDimension(dimension Dimension) error {
	switch dimension.Scope {
	case ScopeGlobal:
		if dimension.SubjectID != "" {
			return fmt.Errorf("%w: global dimension cannot have a subject ID", ErrInvalidInput)
		}
	case ScopeResourceDomain, ScopeUser, ScopeEntitlement, ScopeGatewayKey, ScopeModel, ScopeProvider, ScopeCredential:
		if strings.TrimSpace(dimension.SubjectID) == "" {
			return fmt.Errorf("%w: %s dimension requires a subject ID", ErrInvalidInput, dimension.Scope)
		}
		if len(dimension.SubjectID) > maximumSubjectBytes {
			return fmt.Errorf("%w: subject ID exceeds %d bytes", ErrInvalidInput, maximumSubjectBytes)
		}
	default:
		return fmt.Errorf("%w: unsupported dimension scope %q", ErrInvalidInput, dimension.Scope)
	}
	return nil
}

func unavailable(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrUnavailable, operation, err)
}
