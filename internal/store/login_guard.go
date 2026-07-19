package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var loginLimitScript = redis.NewScript(`
local attempts = redis.call('INCR', KEYS[1])
if attempts == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
return {attempts, ttl}
`)

type LoginGuard struct {
	client          *redis.Client
	accountAttempts int64
	addressAttempts int64
	window          time.Duration
}

func NewLoginGuard(client *redis.Client, accountAttempts, addressAttempts int, window time.Duration) *LoginGuard {
	return &LoginGuard{client: client, accountAttempts: int64(accountAttempts), addressAttempts: int64(addressAttempts), window: window}
}

func (g *LoginGuard) Check(ctx context.Context, account, address string) (time.Duration, error) {
	checks := []struct {
		key   string
		limit int64
	}{
		{key: loginKey("account", strings.ToLower(strings.TrimSpace(account))), limit: g.accountAttempts},
		{key: loginKey("address", address), limit: g.addressAttempts},
	}
	var retryAfter time.Duration
	for _, check := range checks {
		result, err := loginLimitScript.Run(ctx, g.client, []string{check.key}, g.window.Milliseconds()).Int64Slice()
		if err != nil {
			return 0, fmt.Errorf("login rate limit: %w", err)
		}
		if len(result) != 2 {
			return 0, fmt.Errorf("login rate limit returned an invalid result")
		}
		if result[0] > check.limit {
			remaining := time.Duration(result[1]) * time.Millisecond
			if remaining > retryAfter {
				retryAfter = remaining
			}
		}
	}
	return retryAfter, nil
}

func (g *LoginGuard) Reset(ctx context.Context, account string) error {
	return g.client.Del(ctx, loginKey("account", strings.ToLower(strings.TrimSpace(account)))).Err()
}

func loginKey(kind, value string) string {
	digest := sha256.Sum256([]byte(value))
	return "llmgateway:login:{" + kind + "}:" + hex.EncodeToString(digest[:])
}
