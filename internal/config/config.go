package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Profile string

const (
	ProfileDevelopment Profile = "development"
	ProfileTest        Profile = "test"
	ProfileProduction  Profile = "production"
)

type Config struct {
	Profile       Profile
	HTTP          HTTP
	Database      Database
	Valkey        Valkey
	Security      Security
	ProviderProbe ProviderProbe
	RequestFlow   RequestFlow
	Responses     Responses
	Logging       Logging
}

type Responses struct {
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	StaleAfter        time.Duration
	RecoveryBatchSize int32
	MaxWorkers        int
}

type ProviderProbe struct {
	Timeout          time.Duration
	MaxResponseBytes int64
}

type HTTP struct {
	Address           string
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxBodyBytes      int64
}

type Database struct {
	URL            string
	MaxConnections int32
	MinConnections int32
	ConnectTimeout time.Duration
	MigrateOnStart bool
}

type Valkey struct {
	Address        string
	Password       string
	Database       int
	ConnectTimeout time.Duration
}

type Security struct {
	MasterKeys              map[uint32][]byte
	ActiveMasterKeyVersion  uint32
	SessionPepper           []byte
	APIKeyPepper            []byte
	CoordinationKeyHash     []byte
	ProviderCABundleFile    string
	CookieSecure            bool
	TrustedProxy            string
	LoginAccountAttempts    int
	LoginAddressAttempts    int
	LoginWindow             time.Duration
	AllowedPrivatePrefixes  []netip.Prefix
	AllowedResolvedPrefixes []netip.Prefix
}

type Capacity struct {
	RequestsPerMinute int64
	TokensPerMinute   int64
	Concurrency       int64
}

type RequestFlow struct {
	MaxResponseBytes           int64
	ExecutionHeartbeatInterval time.Duration
	ExecutionStaleAfter        time.Duration
	RecoveryInterval           time.Duration
	RecoveryBatchSize          int32
	MaxQueued                  int
	MaxActive                  int
	MaxActivePerUser           int
	MaxQueueWait               time.Duration
	AdmissionRetryInterval     time.Duration
	LeaseTTL                   time.Duration
	RetryMaxAttempts           int
	RetryMaxElapsed            time.Duration
	RetryInitialBackoff        time.Duration
	RetryMaximumBackoff        time.Duration
	CircuitFailureThreshold    int
	CircuitSuccessThreshold    int
	CircuitOpenDuration        time.Duration
	CircuitHalfOpenMaxInFlight int
	Global                     Capacity
	ResourceDomain             Capacity
	User                       Capacity
	GatewayKey                 Capacity
	Model                      Capacity
	Provider                   Capacity
	Credential                 Capacity
}

type Logging struct {
	Level string
}

func Load() (Config, error) {
	profile := Profile(env("LLMGATEWAY_PROFILE", string(ProfileDevelopment)))
	cfg := Config{
		Profile: profile,
		HTTP: HTTP{
			Address:           env("LLMGATEWAY_HTTP_ADDRESS", "127.0.0.1:8080"),
			ReadHeaderTimeout: durationEnv("LLMGATEWAY_HTTP_READ_HEADER_TIMEOUT", 10*time.Second),
			IdleTimeout:       durationEnv("LLMGATEWAY_HTTP_IDLE_TIMEOUT", 90*time.Second),
			ShutdownTimeout:   durationEnv("LLMGATEWAY_HTTP_SHUTDOWN_TIMEOUT", 30*time.Second),
			MaxBodyBytes:      int64Env("LLMGATEWAY_HTTP_MAX_BODY_BYTES", 4<<20),
		},
		Database: Database{
			URL:            env("LLMGATEWAY_DATABASE_URL", "postgres://llmgateway:llmgateway_dev@127.0.0.1:15432/llmgateway?sslmode=disable"),
			MaxConnections: int32(intEnv("LLMGATEWAY_DATABASE_MAX_CONNECTIONS", 20)),
			MinConnections: int32(intEnv("LLMGATEWAY_DATABASE_MIN_CONNECTIONS", 2)),
			ConnectTimeout: durationEnv("LLMGATEWAY_DATABASE_CONNECT_TIMEOUT", 10*time.Second),
			MigrateOnStart: boolEnv("LLMGATEWAY_DATABASE_MIGRATE_ON_START", profile != ProfileProduction),
		},
		Valkey: Valkey{
			Address:        env("LLMGATEWAY_VALKEY_ADDRESS", "127.0.0.1:16380"),
			Password:       env("LLMGATEWAY_VALKEY_PASSWORD", "llmgateway_dev"),
			Database:       intEnv("LLMGATEWAY_VALKEY_DATABASE", 0),
			ConnectTimeout: durationEnv("LLMGATEWAY_VALKEY_CONNECT_TIMEOUT", 5*time.Second),
		},
		Security: Security{
			MasterKeys:              masterKeysEnv("LLMGATEWAY_MASTER_KEYS", profile),
			ActiveMasterKeyVersion:  uint32(intEnv("LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION", 1)),
			SessionPepper:           []byte(env("LLMGATEWAY_SESSION_PEPPER", developmentSecret(profile, "llmgateway-development-session-pepper"))),
			APIKeyPepper:            []byte(env("LLMGATEWAY_API_KEY_PEPPER", developmentSecret(profile, "llmgateway-development-api-key-pepper"))),
			CoordinationKeyHash:     []byte(env("LLMGATEWAY_COORDINATION_KEY_HASH_SECRET", developmentSecret(profile, "llmgateway-development-coordination-key-hash-secret"))),
			ProviderCABundleFile:    strings.TrimSpace(os.Getenv("LLMGATEWAY_PROVIDER_CA_BUNDLE_FILE")),
			CookieSecure:            boolEnv("LLMGATEWAY_COOKIE_SECURE", profile == ProfileProduction),
			TrustedProxy:            strings.TrimSpace(os.Getenv("LLMGATEWAY_TRUSTED_PROXY")),
			LoginAccountAttempts:    intEnv("LLMGATEWAY_LOGIN_ACCOUNT_ATTEMPTS", 5),
			LoginAddressAttempts:    intEnv("LLMGATEWAY_LOGIN_ADDRESS_ATTEMPTS", 30),
			LoginWindow:             durationEnv("LLMGATEWAY_LOGIN_WINDOW", 10*time.Minute),
			AllowedPrivatePrefixes:  prefixListEnv("LLMGATEWAY_ALLOWED_PRIVATE_NETWORKS"),
			AllowedResolvedPrefixes: prefixListEnv("LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS"),
		},
		ProviderProbe: ProviderProbe{
			Timeout:          durationEnv("LLMGATEWAY_PROVIDER_PROBE_TIMEOUT", 15*time.Second),
			MaxResponseBytes: int64Env("LLMGATEWAY_PROVIDER_PROBE_MAX_RESPONSE_BYTES", 1<<20),
		},
		RequestFlow: RequestFlow{
			MaxResponseBytes:           int64Env("LLMGATEWAY_REQUEST_MAX_RESPONSE_BYTES", 16<<20),
			ExecutionHeartbeatInterval: durationEnv("LLMGATEWAY_REQUEST_EXECUTION_HEARTBEAT_INTERVAL", 10*time.Second),
			ExecutionStaleAfter:        durationEnv("LLMGATEWAY_REQUEST_EXECUTION_STALE_AFTER", time.Minute),
			RecoveryInterval:           durationEnv("LLMGATEWAY_REQUEST_RECOVERY_INTERVAL", 15*time.Second),
			RecoveryBatchSize:          int32(intEnv("LLMGATEWAY_REQUEST_RECOVERY_BATCH_SIZE", 100)),
			MaxQueued:                  intEnv("LLMGATEWAY_REQUEST_MAX_QUEUED", 1024),
			MaxActive:                  intEnv("LLMGATEWAY_REQUEST_MAX_ACTIVE", 256),
			MaxActivePerUser:           intEnv("LLMGATEWAY_REQUEST_MAX_ACTIVE_PER_USER", 16),
			MaxQueueWait:               durationEnv("LLMGATEWAY_REQUEST_MAX_QUEUE_WAIT", 30*time.Second),
			AdmissionRetryInterval:     durationEnv("LLMGATEWAY_REQUEST_ADMISSION_RETRY_INTERVAL", 100*time.Millisecond),
			LeaseTTL:                   durationEnv("LLMGATEWAY_REQUEST_LEASE_TTL", 30*time.Second),
			RetryMaxAttempts:           intEnv("LLMGATEWAY_REQUEST_RETRY_MAX_ATTEMPTS", 2),
			RetryMaxElapsed:            durationEnv("LLMGATEWAY_REQUEST_RETRY_MAX_ELAPSED", 30*time.Second),
			RetryInitialBackoff:        durationEnv("LLMGATEWAY_REQUEST_RETRY_INITIAL_BACKOFF", 100*time.Millisecond),
			RetryMaximumBackoff:        durationEnv("LLMGATEWAY_REQUEST_RETRY_MAXIMUM_BACKOFF", 2*time.Second),
			CircuitFailureThreshold:    intEnv("LLMGATEWAY_REQUEST_CIRCUIT_FAILURE_THRESHOLD", 3),
			CircuitSuccessThreshold:    intEnv("LLMGATEWAY_REQUEST_CIRCUIT_SUCCESS_THRESHOLD", 1),
			CircuitOpenDuration:        durationEnv("LLMGATEWAY_REQUEST_CIRCUIT_OPEN_DURATION", 30*time.Second),
			CircuitHalfOpenMaxInFlight: intEnv("LLMGATEWAY_REQUEST_CIRCUIT_HALF_OPEN_MAX_IN_FLIGHT", 1),
			Global:                     capacityEnv("GLOBAL", Capacity{RequestsPerMinute: 6000, TokensPerMinute: 6_000_000, Concurrency: 256}),
			ResourceDomain:             capacityEnv("RESOURCE_DOMAIN", Capacity{RequestsPerMinute: 3000, TokensPerMinute: 3_000_000, Concurrency: 128}),
			User:                       capacityEnv("USER", Capacity{RequestsPerMinute: 600, TokensPerMinute: 600_000, Concurrency: 16}),
			GatewayKey:                 capacityEnv("GATEWAY_KEY", Capacity{RequestsPerMinute: 300, TokensPerMinute: 300_000, Concurrency: 8}),
			Model:                      capacityEnv("MODEL", Capacity{RequestsPerMinute: 3000, TokensPerMinute: 3_000_000, Concurrency: 128}),
			Provider:                   capacityEnv("PROVIDER", Capacity{RequestsPerMinute: 3000, TokensPerMinute: 3_000_000, Concurrency: 128}),
			Credential:                 capacityEnv("CREDENTIAL", Capacity{RequestsPerMinute: 60, TokensPerMinute: 100_000, Concurrency: 4}),
		},
		Responses: Responses{
			PollInterval:      durationEnv("LLMGATEWAY_RESPONSES_POLL_INTERVAL", 500*time.Millisecond),
			HeartbeatInterval: durationEnv("LLMGATEWAY_RESPONSES_HEARTBEAT_INTERVAL", 5*time.Second),
			StaleAfter:        durationEnv("LLMGATEWAY_RESPONSES_STALE_AFTER", 30*time.Second),
			RecoveryBatchSize: int32(intEnv("LLMGATEWAY_RESPONSES_RECOVERY_BATCH_SIZE", 100)),
			MaxWorkers:        intEnv("LLMGATEWAY_RESPONSES_MAX_WORKERS", 8),
		},
		Logging: Logging{Level: env("LLMGATEWAY_LOG_LEVEL", "info")},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []error
	if c.Profile != ProfileDevelopment && c.Profile != ProfileTest && c.Profile != ProfileProduction {
		problems = append(problems, fmt.Errorf("LLMGATEWAY_PROFILE must be development, test, or production"))
	}
	if _, _, err := net.SplitHostPort(c.HTTP.Address); err != nil {
		problems = append(problems, fmt.Errorf("LLMGATEWAY_HTTP_ADDRESS: %w", err))
	}
	if c.Database.URL == "" {
		problems = append(problems, errors.New("LLMGATEWAY_DATABASE_URL is required"))
	}
	if c.Database.MaxConnections < 1 || c.Database.MinConnections < 0 || c.Database.MinConnections > c.Database.MaxConnections {
		problems = append(problems, errors.New("database connection bounds are invalid"))
	}
	if _, _, err := net.SplitHostPort(c.Valkey.Address); err != nil {
		problems = append(problems, fmt.Errorf("LLMGATEWAY_VALKEY_ADDRESS: %w", err))
	}
	if len(c.Security.MasterKeys) == 0 {
		problems = append(problems, errors.New("LLMGATEWAY_MASTER_KEYS must contain at least one versioned key"))
	}
	for version, key := range c.Security.MasterKeys {
		if version == 0 || len(key) != 32 {
			problems = append(problems, fmt.Errorf("master key version %d must decode to exactly 32 bytes", version))
		}
	}
	if _, ok := c.Security.MasterKeys[c.Security.ActiveMasterKeyVersion]; !ok {
		problems = append(problems, errors.New("LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION must select a configured key"))
	}
	if len(c.Security.SessionPepper) < 32 {
		problems = append(problems, errors.New("LLMGATEWAY_SESSION_PEPPER must contain at least 32 bytes"))
	}
	if len(c.Security.APIKeyPepper) < 32 {
		problems = append(problems, errors.New("LLMGATEWAY_API_KEY_PEPPER must contain at least 32 bytes"))
	}
	if len(c.Security.CoordinationKeyHash) < 32 {
		problems = append(problems, errors.New("LLMGATEWAY_COORDINATION_KEY_HASH_SECRET must contain at least 32 bytes"))
	}
	if c.Security.LoginAccountAttempts < 1 || c.Security.LoginAddressAttempts < c.Security.LoginAccountAttempts || c.Security.LoginWindow < time.Minute {
		problems = append(problems, errors.New("login rate limit settings are invalid"))
	}
	if c.Security.AllowedPrivatePrefixes == nil {
		problems = append(problems, errors.New("LLMGATEWAY_ALLOWED_PRIVATE_NETWORKS contains an invalid CIDR"))
	}
	if c.Security.AllowedResolvedPrefixes == nil {
		problems = append(problems, errors.New("LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS contains an invalid CIDR"))
	}
	if c.ProviderProbe.Timeout <= 0 || c.ProviderProbe.Timeout > 5*time.Minute || c.ProviderProbe.MaxResponseBytes < 1024 || c.ProviderProbe.MaxResponseBytes > 16<<20 {
		problems = append(problems, errors.New("provider probe bounds are invalid"))
	}
	if c.Profile == ProfileProduction {
		if strings.HasPrefix(c.HTTP.Address, "127.0.0.1:") && c.Security.TrustedProxy != "" {
			problems = append(problems, errors.New("trusted proxy cannot be enabled with a loopback-only listener"))
		}
		if !c.Security.CookieSecure {
			problems = append(problems, errors.New("secure cookies are required in production"))
		}
	}
	if c.RequestFlow.MaxResponseBytes < 1024 || c.RequestFlow.ExecutionHeartbeatInterval <= 0 ||
		c.RequestFlow.ExecutionStaleAfter <= 2*c.RequestFlow.ExecutionHeartbeatInterval ||
		c.RequestFlow.RecoveryInterval <= 0 || c.RequestFlow.RecoveryInterval > c.RequestFlow.ExecutionStaleAfter ||
		c.RequestFlow.RecoveryBatchSize < 1 || c.RequestFlow.RecoveryBatchSize > 1000 ||
		c.RequestFlow.MaxQueued < 1 || c.RequestFlow.MaxActive < 1 ||
		c.RequestFlow.MaxActivePerUser < 1 || c.RequestFlow.MaxActivePerUser > c.RequestFlow.MaxActive || c.RequestFlow.MaxQueueWait <= 0 ||
		c.RequestFlow.AdmissionRetryInterval < 10*time.Millisecond || c.RequestFlow.AdmissionRetryInterval > time.Second ||
		c.RequestFlow.LeaseTTL < 3*time.Second || c.RequestFlow.LeaseTTL > time.Hour ||
		c.RequestFlow.RetryMaxAttempts < 1 || c.RequestFlow.RetryMaxAttempts > 1000 || c.RequestFlow.RetryMaxElapsed <= 0 ||
		c.RequestFlow.RetryInitialBackoff <= 0 || c.RequestFlow.RetryMaximumBackoff < c.RequestFlow.RetryInitialBackoff ||
		c.RequestFlow.CircuitFailureThreshold < 1 || c.RequestFlow.CircuitSuccessThreshold < 1 ||
		c.RequestFlow.CircuitOpenDuration <= 0 || c.RequestFlow.CircuitHalfOpenMaxInFlight < 1 {
		problems = append(problems, errors.New("request workflow timing and resilience settings are invalid"))
	}
	for _, capacity := range []Capacity{c.RequestFlow.Global, c.RequestFlow.ResourceDomain, c.RequestFlow.User, c.RequestFlow.GatewayKey, c.RequestFlow.Model, c.RequestFlow.Provider, c.RequestFlow.Credential} {
		if capacity.RequestsPerMinute < 1 || capacity.TokensPerMinute < 1 || capacity.Concurrency < 1 {
			problems = append(problems, errors.New("request workflow capacities must be positive"))
			break
		}
	}
	if c.Responses.PollInterval <= 0 || c.Responses.PollInterval > c.Responses.StaleAfter ||
		c.Responses.HeartbeatInterval <= 0 || c.Responses.StaleAfter <= 2*c.Responses.HeartbeatInterval ||
		c.Responses.RecoveryBatchSize < 1 || c.Responses.RecoveryBatchSize > 1000 ||
		c.Responses.MaxWorkers < 1 || c.Responses.MaxWorkers > 1000 {
		problems = append(problems, errors.New("background response execution settings are invalid"))
	}
	return errors.Join(problems...)
}

func (c Config) LogLevel() slog.Level {
	switch strings.ToLower(c.Logging.Level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func developmentSecret(profile Profile, value string) string {
	if profile == ProfileProduction {
		return ""
	}
	return value
}

func masterKeysEnv(key string, profile Profile) map[uint32][]byte {
	value := env(key, developmentSecret(profile, "1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="))
	keys := make(map[uint32][]byte)
	for _, item := range strings.Split(value, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) != 2 {
			return nil
		}
		version, err := strconv.ParseUint(parts[0], 10, 32)
		if err != nil || version == 0 {
			return nil
		}
		decoded, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
		keys[uint32(version)] = decoded
	}
	return keys
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return -1
	}
	return parsed
}

func intEnv(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return -1
	}
	return parsed
}

func int64Env(key string, fallback int64) int64 {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return -1
	}
	return parsed
}

func capacityEnv(scope string, fallback Capacity) Capacity {
	prefix := "LLMGATEWAY_REQUEST_" + scope + "_"
	return Capacity{
		RequestsPerMinute: int64Env(prefix+"RPM", fallback.RequestsPerMinute),
		TokensPerMinute:   int64Env(prefix+"TPM", fallback.TokensPerMinute),
		Concurrency:       int64Env(prefix+"CONCURRENCY", fallback.Concurrency),
	}
}

func boolEnv(key string, fallback bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return parsed
}

func prefixListEnv(key string) []netip.Prefix {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return []netip.Prefix{}
	}
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes
}
