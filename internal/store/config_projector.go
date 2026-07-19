package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	db "github.com/luckymaomi/llmgateway/internal/store/db"
	"github.com/redis/go-redis/v9"
)

const activeConfigPointerKey = "llmgateway:{configuration}:active"

var activateConfigScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current or tonumber(current) < tonumber(ARGV[1]) then
  redis.call('SET', KEYS[2], ARGV[2])
  redis.call('SET', KEYS[1], ARGV[1])
  return 1
end
return 0
`)

type ConfigProjector struct {
	queries *db.Queries
	valkey  *redis.Client
	period  time.Duration
}

type projectedConfig struct {
	RevisionID string          `json:"revision_id"`
	Version    int64           `json:"version"`
	Document   json.RawMessage `json:"document"`
}

func NewConfigProjector(connections *Connections) *ConfigProjector {
	return &ConfigProjector{queries: db.New(connections.Postgres), valkey: connections.Valkey, period: time.Second}
}

func (p *ConfigProjector) Run(ctx context.Context) {
	ticker := time.NewTicker(p.period)
	defer ticker.Stop()
	for {
		_ = p.ProjectOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *ConfigProjector) ProjectOnce(ctx context.Context) error {
	items, err := p.queries.ListPendingConfigOutbox(ctx, 100)
	if err != nil {
		return err
	}
	for _, item := range items {
		projection, err := json.Marshal(projectedConfig{RevisionID: item.RevisionID.String(), Version: item.ActiveVersion, Document: item.Document})
		if err == nil {
			snapshotKey := "llmgateway:{configuration}:snapshot:" + strconv.FormatInt(item.ActiveVersion, 10)
			_, err = activateConfigScript.Run(ctx, p.valkey, []string{activeConfigPointerKey, snapshotKey}, item.ActiveVersion, projection).Result()
		}
		if err != nil {
			detail := err.Error()
			if len(detail) > 500 {
				detail = detail[:500]
			}
			_ = p.queries.MarkConfigOutboxFailed(ctx, db.MarkConfigOutboxFailedParams{ID: item.ID, LastError: &detail})
			return fmt.Errorf("project configuration version %d: %w", item.ActiveVersion, err)
		}
		if err := p.queries.MarkConfigOutboxDelivered(ctx, item.ID); err != nil {
			return err
		}
	}
	return nil
}
