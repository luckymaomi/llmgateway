-- name: GetActiveGatewayKeyForRequest :one
SELECT k.user_id
FROM gateway_keys k
JOIN users u ON u.id = k.user_id
WHERE k.id = sqlc.arg(gateway_key_id)
  AND k.user_id = sqlc.arg(user_id)
  AND k.revoked_at IS NULL
  AND (k.expires_at IS NULL OR k.expires_at > now())
  AND u.status = 'active'
FOR SHARE OF k, u;

-- name: CreateLedgerEvent :one
INSERT INTO ledger_events (user_id, subscription_id, request_id, reservation_id, kind, token_delta, reserved_tokens, input_tokens, output_tokens, usage_source, source_event_id, note, created_by)
VALUES (sqlc.arg(user_id), sqlc.arg(subscription_id), sqlc.narg(request_id), sqlc.narg(reservation_id), sqlc.arg(kind), sqlc.arg(token_delta), sqlc.arg(reserved_tokens), sqlc.arg(input_tokens), sqlc.arg(output_tokens), sqlc.arg(usage_source), sqlc.narg(source_event_id), sqlc.narg(note), sqlc.narg(created_by))
RETURNING *;

-- name: SubscriptionBalance :one
SELECT coalesce(sum(token_delta), 0)::bigint FROM ledger_events WHERE subscription_id = sqlc.arg(subscription_id);

-- name: ListLedgerEventsBySubscription :many
SELECT * FROM ledger_events WHERE subscription_id = sqlc.arg(subscription_id) ORDER BY created_at, id;

-- name: CreateLedgerReservation :one
INSERT INTO ledger_reservations (id, subscription_id, request_id, state, reserved_tokens, reserve_event_id)
VALUES (sqlc.arg(id), sqlc.arg(subscription_id), sqlc.arg(request_id), 'reserved', sqlc.arg(reserved_tokens), sqlc.arg(reserve_event_id))
RETURNING *;

-- name: GetLedgerReservationForUpdate :one
SELECT * FROM ledger_reservations WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: GetLedgerReservationByRequest :one
SELECT * FROM ledger_reservations WHERE request_id = sqlc.arg(request_id);

-- name: GetLedgerReservationByRequestForUpdate :one
SELECT * FROM ledger_reservations WHERE request_id = sqlc.arg(request_id) FOR UPDATE;

-- name: CompleteLedgerReservation :one
UPDATE ledger_reservations
SET state = sqlc.arg(state), charged_tokens = sqlc.arg(charged_tokens), usage_source = sqlc.arg(usage_source), terminal_event_id = sqlc.arg(terminal_event_id), updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'reserved'
RETURNING *;

-- name: CountLedgerEvents :one
SELECT count(*)
FROM ledger_events event
JOIN users owner ON owner.id = event.user_id
JOIN subscriptions subscription ON subscription.id = event.subscription_id
JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
JOIN service_plans plan ON plan.id = version.service_plan_id
LEFT JOIN users actor ON actor.id = event.created_by
WHERE (sqlc.narg(user_id)::uuid IS NULL OR event.user_id = sqlc.narg(user_id))
  AND (sqlc.narg(subscription_id)::uuid IS NULL OR event.subscription_id = sqlc.narg(subscription_id))
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', owner.display_name, owner.email, plan.name, actor.display_name, actor.email, event.note, event.request_id::text) ILIKE '%' || sqlc.arg(search)::text || '%');

-- name: ListLedgerEvents :many
SELECT event.*, plan.name AS service_plan_name, owner.display_name AS owner_name, actor.display_name AS actor_name
FROM ledger_events event
JOIN users owner ON owner.id = event.user_id
JOIN subscriptions subscription ON subscription.id = event.subscription_id
JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
JOIN service_plans plan ON plan.id = version.service_plan_id
LEFT JOIN users actor ON actor.id = event.created_by
WHERE (sqlc.narg(user_id)::uuid IS NULL OR event.user_id = sqlc.narg(user_id))
  AND (sqlc.narg(subscription_id)::uuid IS NULL OR event.subscription_id = sqlc.narg(subscription_id))
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', owner.display_name, owner.email, plan.name, actor.display_name, actor.email, event.note, event.request_id::text) ILIKE '%' || sqlc.arg(search)::text || '%')
ORDER BY event.created_at DESC, event.id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CreateRequest :one
INSERT INTO requests (id, idempotency_key, request_digest, user_id, gateway_key_id, model_id, subscription_id, resource_pool_id,
                      price_version_id, cost_currency, input_rate_nanos_per_million, output_rate_nanos_per_million, status, stream)
VALUES (sqlc.arg(id), sqlc.narg(idempotency_key), sqlc.arg(request_digest), sqlc.arg(user_id), sqlc.arg(gateway_key_id), sqlc.arg(model_id), sqlc.arg(subscription_id), sqlc.arg(resource_pool_id),
        sqlc.arg(price_version_id), sqlc.arg(cost_currency), sqlc.arg(input_rate_nanos_per_million), sqlc.arg(output_rate_nanos_per_million), sqlc.arg(status), sqlc.arg(stream))
RETURNING *;

-- name: GetRequestByIdempotencyKey :one
SELECT * FROM requests WHERE gateway_key_id = sqlc.arg(gateway_key_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: GetRequest :one
SELECT * FROM requests WHERE id = sqlc.arg(id);

-- name: GetRequestForUpdate :one
SELECT * FROM requests WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ClaimRequestExecution :one
UPDATE requests
SET status = 'dispatching',
    execution_id = sqlc.arg(execution_id),
    execution_generation = CASE WHEN execution_id IS NULL THEN execution_generation + 1 ELSE execution_generation END,
    execution_claimed_at = CASE WHEN execution_id IS NULL THEN now() ELSE execution_claimed_at END,
    execution_heartbeat_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND (
      (status = 'queued' AND execution_id IS NULL)
      OR
      (status = 'dispatching' AND execution_id = sqlc.arg(execution_id))
  )
RETURNING *;

-- name: HeartbeatRequestExecution :one
UPDATE requests
SET execution_heartbeat_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status IN ('dispatching', 'streaming')
RETURNING *;

-- name: MarkRequestExecutionStreaming :one
UPDATE requests
SET status = 'streaming', execution_heartbeat_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status IN ('dispatching', 'streaming')
RETURNING *;

-- name: MarkRequestExecutionUncertain :one
UPDATE requests
SET status = 'uncertain', error_kind = sqlc.arg(error_kind), error_detail = sqlc.arg(error_detail),
    completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status IN ('dispatching', 'streaming')
RETURNING *;

-- name: RecoverStaleRequestExecutions :one
WITH stale AS (
    SELECT candidate.id, candidate.execution_id, candidate.execution_generation
    FROM requests AS candidate
    WHERE candidate.status IN ('dispatching', 'streaming')
      AND candidate.execution_heartbeat_at < sqlc.arg(stale_before)
      AND NOT EXISTS (
          SELECT 1 FROM request_attempts completed_attempt
          WHERE completed_attempt.request_id = candidate.id
            AND completed_attempt.execution_id = candidate.execution_id
            AND completed_attempt.execution_generation = candidate.execution_generation
            AND completed_attempt.status = 'completed'
            AND completed_attempt.input_tokens IS NOT NULL
            AND completed_attempt.output_tokens IS NOT NULL
            AND completed_attempt.usage_source IN ('authoritative', 'estimated')
      )
    ORDER BY candidate.execution_heartbeat_at, candidate.id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)
), fenced_attempts AS (
    UPDATE request_attempts AS attempt
    SET status = 'uncertain', error_kind = 'uncertain', completed_at = now()
    FROM stale
    WHERE attempt.request_id = stale.id
      AND attempt.execution_id = stale.execution_id
      AND attempt.execution_generation = stale.execution_generation
      AND attempt.status IN ('created', 'sending', 'streaming')
), fenced_requests AS (
    UPDATE requests AS request
    SET status = 'uncertain', error_kind = 'uncertain',
        error_detail = 'request execution heartbeat expired; upstream outcome requires recovery',
        completed_at = now(), updated_at = now()
    FROM stale
    WHERE request.id = stale.id
      AND request.execution_id = stale.execution_id
      AND request.execution_generation = stale.execution_generation
      AND request.status IN ('dispatching', 'streaming')
    RETURNING request.id
)
SELECT count(*)::bigint FROM fenced_requests;

-- name: ListRecoverableRequestSettlements :many
SELECT request.id AS request_id,
       request.execution_id,
       request.execution_generation,
       completed_attempt.input_tokens,
       completed_attempt.output_tokens,
       completed_attempt.usage_source
FROM requests AS request
JOIN ledger_reservations AS reservation
  ON reservation.request_id = request.id AND reservation.state = 'reserved'
JOIN LATERAL (
    SELECT attempt.input_tokens, attempt.output_tokens, attempt.usage_source
    FROM request_attempts AS attempt
    WHERE attempt.request_id = request.id
      AND attempt.execution_id = request.execution_id
      AND attempt.execution_generation = request.execution_generation
      AND attempt.status = 'completed'
      AND attempt.input_tokens IS NOT NULL
      AND attempt.output_tokens IS NOT NULL
      AND attempt.usage_source IN ('authoritative', 'estimated')
    ORDER BY attempt.sequence DESC
    LIMIT 1
) AS completed_attempt ON true
WHERE request.status IN ('dispatching', 'streaming')
  AND request.execution_id IS NOT NULL
  AND request.execution_heartbeat_at < sqlc.arg(stale_before)
ORDER BY request.execution_heartbeat_at, request.id
LIMIT sqlc.arg(batch_size);

-- name: ListStaleQueuedRequests :many
SELECT request.id
FROM requests AS request
JOIN ledger_reservations AS reservation
  ON reservation.request_id = request.id AND reservation.state = 'reserved'
WHERE request.status = 'queued'
  AND request.execution_id IS NULL
  AND request.execution_generation = 0
  AND request.updated_at < sqlc.arg(stale_before)
ORDER BY request.updated_at, request.id
LIMIT sqlc.arg(batch_size);

-- name: CompleteRequest :one
UPDATE requests SET status = 'completed', input_tokens = sqlc.arg(input_tokens), output_tokens = sqlc.arg(output_tokens), usage_source = sqlc.arg(usage_source),
    input_cost_nanos = sqlc.arg(input_cost_nanos), output_cost_nanos = sqlc.arg(output_cost_nanos), total_cost_nanos = sqlc.arg(total_cost_nanos),
    error_kind = NULL, error_detail = NULL, completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status IN ('dispatching', 'streaming')
RETURNING *;

-- name: FailRequest :one
UPDATE requests
SET status = 'failed', error_kind = sqlc.arg(error_kind), error_detail = sqlc.narg(error_detail), completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND status IN ('queued', 'dispatching', 'streaming')
  AND (
      (sqlc.narg(execution_id)::uuid IS NULL AND execution_id IS NULL AND execution_generation = 0)
      OR
      (execution_id = sqlc.narg(execution_id) AND execution_generation = sqlc.arg(execution_generation))
  )
RETURNING *;

-- name: FailRequestWithUsage :one
UPDATE requests
SET status = 'failed', input_tokens = sqlc.arg(input_tokens), output_tokens = sqlc.arg(output_tokens), usage_source = sqlc.arg(usage_source),
    input_cost_nanos = sqlc.arg(input_cost_nanos), output_cost_nanos = sqlc.arg(output_cost_nanos), total_cost_nanos = sqlc.arg(total_cost_nanos),
    error_kind = sqlc.arg(error_kind), error_detail = sqlc.narg(error_detail), completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status IN ('dispatching', 'streaming')
RETURNING *;

-- name: ListRequests :many
SELECT * FROM requests
WHERE (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id))
ORDER BY accepted_at DESC, id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountRequestLogs :one
SELECT count(*)
FROM requests AS request
JOIN users AS owner ON owner.id = request.user_id
JOIN gateway_keys AS key ON key.id = request.gateway_key_id
JOIN models AS model ON model.id = request.model_id
JOIN resource_pools AS pool ON pool.id = request.resource_pool_id
WHERE (sqlc.narg(user_id)::uuid IS NULL OR request.user_id = sqlc.narg(user_id))
  AND (sqlc.narg(gateway_key_id)::uuid IS NULL OR request.gateway_key_id = sqlc.narg(gateway_key_id))
  AND (sqlc.narg(model_id)::uuid IS NULL OR request.model_id = sqlc.narg(model_id))
  AND (sqlc.arg(status)::text = '' OR request.status::text = sqlc.arg(status)::text)
  AND request.accepted_at >= sqlc.arg(from_time)
  AND request.accepted_at < sqlc.arg(to_time)
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', owner.display_name, owner.email, key.prefix, model.public_name, pool.name, request.id::text) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(resource_pool_id)::uuid IS NULL OR request.resource_pool_id = sqlc.narg(resource_pool_id));

-- name: ListRequestLogs :many
SELECT request.id, request.user_id, owner.display_name AS user_name,
       request.gateway_key_id, key.prefix AS key_prefix,
       request.model_id, model.public_name AS model_alias,
       request.resource_pool_id, pool.name AS resource_pool_name, pool.slug AS resource_pool_slug,
       request.status, request.stream, request.input_tokens, request.output_tokens,
       request.usage_source, request.error_kind, request.accepted_at, request.completed_at,
       request.updated_at,
       (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id)::bigint AS attempt_count,
       coalesce((SELECT attempt.status::text FROM request_attempts attempt WHERE attempt.request_id = request.id ORDER BY attempt.sequence DESC, attempt.id DESC LIMIT 1), ''::text)::text AS last_attempt_status
FROM requests AS request
JOIN users AS owner ON owner.id = request.user_id
JOIN gateway_keys AS key ON key.id = request.gateway_key_id
JOIN models AS model ON model.id = request.model_id
JOIN resource_pools AS pool ON pool.id = request.resource_pool_id
WHERE (sqlc.narg(user_id)::uuid IS NULL OR request.user_id = sqlc.narg(user_id))
  AND (sqlc.narg(gateway_key_id)::uuid IS NULL OR request.gateway_key_id = sqlc.narg(gateway_key_id))
  AND (sqlc.narg(model_id)::uuid IS NULL OR request.model_id = sqlc.narg(model_id))
  AND (sqlc.arg(status)::text = '' OR request.status::text = sqlc.arg(status)::text)
  AND request.accepted_at >= sqlc.arg(from_time)
  AND request.accepted_at < sqlc.arg(to_time)
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', owner.display_name, owner.email, key.prefix, model.public_name, pool.name, request.id::text) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(resource_pool_id)::uuid IS NULL OR request.resource_pool_id = sqlc.narg(resource_pool_id))
ORDER BY request.accepted_at DESC, request.id DESC
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: GetRequestLog :one
SELECT request.id, request.user_id, owner.display_name AS user_name,
       request.gateway_key_id, key.prefix AS key_prefix,
       request.model_id, model.public_name AS model_alias,
       request.resource_pool_id, pool.name AS resource_pool_name, pool.slug AS resource_pool_slug,
       request.status, request.stream, request.input_tokens, request.output_tokens,
       request.usage_source, request.error_kind, request.accepted_at, request.completed_at,
       request.updated_at,
       (SELECT count(*) FROM request_attempts attempt WHERE attempt.request_id = request.id)::bigint AS attempt_count,
       coalesce((SELECT attempt.status::text FROM request_attempts attempt WHERE attempt.request_id = request.id ORDER BY attempt.sequence DESC, attempt.id DESC LIMIT 1), ''::text)::text AS last_attempt_status
FROM requests AS request
JOIN users AS owner ON owner.id = request.user_id
JOIN gateway_keys AS key ON key.id = request.gateway_key_id
JOIN models AS model ON model.id = request.model_id
JOIN resource_pools AS pool ON pool.id = request.resource_pool_id
WHERE request.id = sqlc.arg(request_id)
  AND (sqlc.narg(user_id)::uuid IS NULL OR request.user_id = sqlc.narg(user_id));

-- name: ListRequestLogAttempts :many
SELECT attempt.id, attempt.sequence, attempt.status, provider.name AS provider_name,
       credential.name AS credential_name, attempt.upstream_request_id, attempt.http_status,
       attempt.error_kind, attempt.retry_after_at, attempt.sent_at, attempt.first_byte_at,
       attempt.completed_at, attempt.input_tokens, attempt.output_tokens,
       attempt.usage_source, attempt.created_at
FROM request_attempts AS attempt
JOIN provider_credentials AS credential ON credential.id = attempt.credential_id
JOIN resource_pools AS pool ON pool.id = credential.resource_pool_id
JOIN providers AS provider ON provider.id = pool.provider_id
WHERE attempt.request_id = sqlc.arg(request_id)
ORDER BY attempt.sequence, attempt.id;

-- name: CreateAttempt :one
INSERT INTO request_attempts (request_id, execution_id, execution_generation, credential_id, sequence, status)
SELECT sqlc.arg(request_id), sqlc.arg(execution_id), sqlc.arg(execution_generation), sqlc.arg(credential_id), sqlc.arg(sequence), sqlc.arg(status)
FROM requests
WHERE id = sqlc.arg(request_id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status IN ('dispatching', 'streaming')
RETURNING *;

-- name: UpdateAttempt :one
UPDATE request_attempts AS attempt
SET status = sqlc.arg(status), upstream_request_id = sqlc.narg(upstream_request_id), http_status = sqlc.narg(http_status),
    error_kind = sqlc.narg(error_kind), retry_after_at = sqlc.narg(retry_after_at),
    sent_at = coalesce(sent_at, sqlc.narg(sent_at)), first_byte_at = coalesce(first_byte_at, sqlc.narg(first_byte_at)),
    completed_at = sqlc.narg(completed_at), input_tokens = sqlc.narg(input_tokens), output_tokens = sqlc.narg(output_tokens),
    usage_source = sqlc.arg(usage_source)
WHERE attempt.id = sqlc.arg(id)
  AND attempt.request_id = sqlc.arg(request_id)
  AND attempt.execution_id = sqlc.arg(execution_id)
  AND attempt.execution_generation = sqlc.arg(execution_generation)
  AND EXISTS (
      SELECT 1 FROM requests request
      WHERE request.id = attempt.request_id
        AND request.execution_id = attempt.execution_id
        AND request.execution_generation = attempt.execution_generation
        AND request.status IN ('dispatching', 'streaming')
  )
  AND CASE sqlc.arg(status)::attempt_status
      WHEN 'sending' THEN attempt.status = 'created'
      WHEN 'streaming' THEN attempt.status IN ('sending', 'streaming')
      WHEN 'completed' THEN attempt.status IN ('sending', 'streaming')
      WHEN 'failed' THEN attempt.status IN ('created', 'sending', 'streaming')
      WHEN 'uncertain' THEN attempt.status IN ('created', 'sending', 'streaming')
      ELSE false
  END
RETURNING attempt.*;

-- name: ListRequestAttempts :many
SELECT * FROM request_attempts WHERE request_id = sqlc.arg(request_id) ORDER BY sequence, id;
