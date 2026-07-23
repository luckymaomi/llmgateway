package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/luckymaomi/llmgateway/internal/security"
)

type virtualUser struct {
	ID     uuid.UUID
	KeyID  uuid.UUID
	Secret string
}

type databaseSummary struct {
	Users               int64            `json:"users"`
	Requests            int64            `json:"requests"`
	RequestStatuses     map[string]int64 `json:"requestStatuses"`
	ReservationStates   map[string]int64 `json:"reservationStates"`
	NonTerminalRequests int64            `json:"nonTerminalRequests"`
	ReservedHolds       int64            `json:"reservedHolds"`
}

type faultDatabaseFact struct {
	Found       bool   `json:"found"`
	Request     string `json:"request,omitempty"`
	Reservation string `json:"reservation,omitempty"`
	Attempts    int64  `json:"attempts"`
}

func provisionUsers(ctx context.Context, pool *pgxpool.Pool, pepper []byte, modelID, planVersionID uuid.UUID, count int, runID string) ([]virtualUser, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	users := make([]virtualUser, 0, count)
	for index := 0; index < count; index++ {
		userID, keyID, subscriptionID := uuid.New(), uuid.New(), uuid.New()
		secret := fmt.Sprintf("llmg_capacity_%s_%03d", runID, index)
		digest, err := security.HMACSHA256(pepper, []byte(secret))
		if err != nil {
			return nil, err
		}
		email := fmt.Sprintf("capacity-%s-%03d@example.test", runID, index)
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, display_name, password_hash, role, status)
VALUES ($1, $2, $3, 'capacity-fixture-cannot-login', 'member', 'active')`, userID, email, fmt.Sprintf("Capacity User %03d", index)); err != nil {
			return nil, fmt.Errorf("insert capacity user %d: %w", index, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO gateway_keys (id, user_id, name, prefix, secret_digest)
VALUES ($1, $2, 'Capacity acceptance', $3, $4)`, keyID, userID, secret[:13], digest[:]); err != nil {
			return nil, fmt.Errorf("insert capacity key %d: %w", index, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO gateway_key_models (gateway_key_id, model_id) VALUES ($1, $2)`, keyID, modelID); err != nil {
			return nil, fmt.Errorf("bind capacity key %d: %w", index, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscriptions
(id, user_id, service_plan_version_id, status, granted_tokens, starts_at, expires_at, assigned_by)
SELECT $1, $2, version.id, 'active', 100000000, $4, $5, version.created_by
FROM service_plan_versions version WHERE version.id = $3`, subscriptionID, userID, planVersionID, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(24*time.Hour)); err != nil {
			return nil, fmt.Errorf("insert capacity subscription %d: %w", index, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_events
(user_id, subscription_id, kind, token_delta, usage_source, source_event_id, note)
VALUES ($1, $2, 'grant', 100000000, 'unknown', $3, 'isolated capacity acceptance')`, userID, subscriptionID, uuid.New()); err != nil {
			return nil, fmt.Errorf("grant capacity subscription %d: %w", index, err)
		}
		users = append(users, virtualUser{ID: userID, KeyID: keyID, Secret: secret})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return users, nil
}

func summarizeDatabase(ctx context.Context, pool *pgxpool.Pool, runID string) (databaseSummary, error) {
	pattern := "capacity-" + runID + "-%"
	summary := databaseSummary{RequestStatuses: map[string]int64{}, ReservationStates: map[string]int64{}}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email LIKE $1`, pattern).Scan(&summary.Users); err != nil {
		return summary, err
	}
	rows, err := pool.Query(ctx, `SELECT r.status::text, count(*)
FROM requests r JOIN users u ON u.id = r.user_id WHERE u.email LIKE $1 GROUP BY r.status`, pattern)
	if err != nil {
		return summary, err
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return summary, err
		}
		summary.RequestStatuses[status] = count
		summary.Requests += count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return summary, err
	}
	rows.Close()
	rows, err = pool.Query(ctx, `SELECT lr.state::text, count(*)
FROM ledger_reservations lr JOIN requests r ON r.id = lr.request_id JOIN users u ON u.id = r.user_id
WHERE u.email LIKE $1 GROUP BY lr.state`, pattern)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return summary, err
		}
		summary.ReservationStates[state] = count
	}
	summary.NonTerminalRequests = summary.RequestStatuses["queued"] + summary.RequestStatuses["dispatching"] + summary.RequestStatuses["streaming"]
	summary.ReservedHolds = summary.ReservationStates["reserved"]
	return summary, rows.Err()
}

func readFaultDatabaseFact(ctx context.Context, pool *pgxpool.Pool, idempotencyKey string) (faultDatabaseFact, error) {
	var fact faultDatabaseFact
	err := pool.QueryRow(ctx, `SELECT r.status::text, lr.state::text,
  (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = r.id)
FROM requests r JOIN ledger_reservations lr ON lr.request_id = r.id
WHERE r.idempotency_key = $1`, idempotencyKey).Scan(&fact.Request, &fact.Reservation, &fact.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return fact, nil
	}
	if err != nil {
		return fact, err
	}
	fact.Found = true
	return fact, nil
}
