package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/luckymaomi/llmgateway/migrations"
	"github.com/redis/go-redis/v9"
)

type Connections struct {
	Postgres *pgxpool.Pool
	Valkey   *redis.Client
}

func Open(ctx context.Context, cfg config.Config) (*Connections, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	poolConfig.MaxConns = cfg.Database.MaxConnections
	poolConfig.MinConns = cfg.Database.MinConnections
	poolConfig.MaxConnIdleTime = 5 * time.Minute

	connectCtx, cancel := context.WithTimeout(ctx, cfg.Database.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if cfg.Database.MigrateOnStart {
		database, err := sql.Open("pgx", cfg.Database.URL)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("open migration connection: %w", err)
		}
		if err := migrations.Up(connectCtx, database); err != nil {
			database.Close()
			pool.Close()
			return nil, err
		}
		if err := database.Close(); err != nil {
			pool.Close()
			return nil, fmt.Errorf("close migration connection: %w", err)
		}
	}

	valkey := redis.NewClient(&redis.Options{
		Addr:         cfg.Valkey.Address,
		Password:     cfg.Valkey.Password,
		DB:           cfg.Valkey.Database,
		DialTimeout:  cfg.Valkey.ConnectTimeout,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	valkeyCtx, valkeyCancel := context.WithTimeout(ctx, cfg.Valkey.ConnectTimeout)
	defer valkeyCancel()
	if err := valkey.Ping(valkeyCtx).Err(); err != nil {
		valkey.Close()
		pool.Close()
		return nil, fmt.Errorf("ping valkey: %w", err)
	}

	return &Connections{Postgres: pool, Valkey: valkey}, nil
}

func (c *Connections) Close() error {
	var valkeyErr error
	if c.Valkey != nil {
		valkeyErr = c.Valkey.Close()
	}
	if c.Postgres != nil {
		c.Postgres.Close()
	}
	return valkeyErr
}

func (c *Connections) Ready(ctx context.Context) error {
	if err := c.Postgres.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if err := c.Valkey.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("valkey: %w", err)
	}
	return nil
}
