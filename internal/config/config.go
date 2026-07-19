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
	Profile  Profile
	HTTP     HTTP
	Database Database
	Valkey   Valkey
	Security Security
	Logging  Logging
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
	CookieSecure            bool
	TrustedProxy            string
	LoginAccountAttempts    int
	LoginAddressAttempts    int
	LoginWindow             time.Duration
	AllowedPrivatePrefixes  []netip.Prefix
	AllowedResolvedPrefixes []netip.Prefix
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
			CookieSecure:            boolEnv("LLMGATEWAY_COOKIE_SECURE", profile == ProfileProduction),
			TrustedProxy:            strings.TrimSpace(os.Getenv("LLMGATEWAY_TRUSTED_PROXY")),
			LoginAccountAttempts:    intEnv("LLMGATEWAY_LOGIN_ACCOUNT_ATTEMPTS", 5),
			LoginAddressAttempts:    intEnv("LLMGATEWAY_LOGIN_ADDRESS_ATTEMPTS", 30),
			LoginWindow:             durationEnv("LLMGATEWAY_LOGIN_WINDOW", 10*time.Minute),
			AllowedPrivatePrefixes:  prefixListEnv("LLMGATEWAY_ALLOWED_PRIVATE_NETWORKS"),
			AllowedResolvedPrefixes: prefixListEnv("LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS"),
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
	if c.Security.LoginAccountAttempts < 1 || c.Security.LoginAddressAttempts < c.Security.LoginAccountAttempts || c.Security.LoginWindow < time.Minute {
		problems = append(problems, errors.New("login rate limit settings are invalid"))
	}
	if c.Security.AllowedPrivatePrefixes == nil {
		problems = append(problems, errors.New("LLMGATEWAY_ALLOWED_PRIVATE_NETWORKS contains an invalid CIDR"))
	}
	if c.Security.AllowedResolvedPrefixes == nil {
		problems = append(problems, errors.New("LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS contains an invalid CIDR"))
	}
	if c.Profile == ProfileProduction {
		if strings.HasPrefix(c.HTTP.Address, "127.0.0.1:") && c.Security.TrustedProxy != "" {
			problems = append(problems, errors.New("trusted proxy cannot be enabled with a loopback-only listener"))
		}
		if !c.Security.CookieSecure {
			problems = append(problems, errors.New("secure cookies are required in production"))
		}
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
