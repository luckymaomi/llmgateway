-- name: GetAdministratorResourceSummary :one
SELECT
  (SELECT count(*) FROM resource_pools WHERE status <> 'retired') AS resource_pool_count,
  (SELECT count(*) FROM resource_pools WHERE status = 'active') AS active_resource_pool_count,
  (SELECT count(DISTINCT provider_id) FROM resource_pools WHERE status <> 'retired') AS connected_provider_count,
  (SELECT count(*) FROM models) AS model_count,
  (SELECT count(*) FROM provider_credentials WHERE status <> 'retired') AS credential_count,
  (SELECT count(*) FROM provider_credentials WHERE status = 'active') AS active_credential_count,
  (SELECT count(*) FROM provider_credentials WHERE status = 'cooling') AS cooling_credential_count,
  (SELECT count(*) FROM provider_credentials WHERE last_probe_status = 'succeeded') AS successful_credential_probe_count,
  (SELECT count(*) FROM users WHERE role = 'member' AND status = 'active') AS active_member_count,
  (SELECT count(*) FROM gateway_keys key JOIN users member ON member.id = key.user_id
   WHERE member.status = 'active' AND key.revoked_at IS NULL
     AND (key.expires_at IS NULL OR key.expires_at > sqlc.arg(observed_at))) AS active_gateway_key_count,
  (SELECT count(*) FROM service_plans WHERE status = 'active' AND current_version_id IS NOT NULL) AS active_service_plan_count,
  (SELECT count(*) FROM subscriptions subscription
   WHERE subscription.status IN ('scheduled', 'active')
     AND subscription.starts_at <= sqlc.arg(observed_at) AND subscription.expires_at > sqlc.arg(observed_at)) AS active_subscription_count,
  EXISTS (
    SELECT 1 FROM resource_pools pool
    JOIN provider_credentials credential ON credential.resource_pool_id = pool.id
    WHERE pool.status = 'active' AND credential.status = 'active'
  ) AS has_active_upstream,
  EXISTS (SELECT 1 FROM model_price_versions WHERE effective_at <= sqlc.arg(observed_at)) AS has_model_price,
  EXISTS (SELECT 1 FROM requests WHERE status = 'completed') AS has_completed_request;

-- name: GetMemberAccessSummary :one
SELECT
  (SELECT count(*) FROM gateway_keys key
   WHERE key.user_id = sqlc.arg(user_id) AND key.revoked_at IS NULL
     AND (key.expires_at IS NULL OR key.expires_at > sqlc.arg(observed_at))) AS active_gateway_key_count,
  (SELECT count(*) FROM subscriptions subscription
   WHERE subscription.user_id = sqlc.arg(user_id)
     AND subscription.status IN ('scheduled', 'active')
     AND subscription.starts_at <= sqlc.arg(observed_at) AND subscription.expires_at > sqlc.arg(observed_at)) AS active_subscription_count,
  COALESCE((
    SELECT sum(balance.tokens)
    FROM subscriptions subscription
    CROSS JOIN LATERAL (
      SELECT COALESCE(sum(event.token_delta), 0)::bigint AS tokens
      FROM ledger_events event WHERE event.subscription_id = subscription.id
    ) balance
    WHERE subscription.user_id = sqlc.arg(user_id)
      AND subscription.status IN ('scheduled', 'active')
      AND subscription.starts_at <= sqlc.arg(observed_at) AND subscription.expires_at > sqlc.arg(observed_at)
  ), 0)::bigint AS remaining_tokens,
  (SELECT min(subscription.expires_at)::timestamptz FROM subscriptions subscription
   WHERE subscription.user_id = sqlc.arg(user_id)
     AND subscription.status IN ('scheduled', 'active')
     AND subscription.starts_at <= sqlc.arg(observed_at) AND subscription.expires_at > sqlc.arg(observed_at)) AS nearest_subscription_expiry;

-- name: GetRequestWindowSummary :one
SELECT count(*) AS request_count,
  count(*) FILTER (WHERE status = 'completed') AS completed_count,
  count(*) FILTER (WHERE status IN ('failed', 'canceled')) AS failed_count,
  count(*) FILTER (WHERE status = 'uncertain') AS uncertain_count,
  COALESCE(sum(input_tokens) FILTER (WHERE input_tokens IS NOT NULL), 0)::bigint AS input_tokens,
  COALESCE(sum(output_tokens) FILTER (WHERE output_tokens IS NOT NULL), 0)::bigint AS output_tokens
FROM requests
WHERE accepted_at >= sqlc.arg(since) AND accepted_at < sqlc.arg(until)
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id));

-- name: GetAttemptLatencySummary :one
SELECT
  COALESCE((percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM (attempt.first_byte_at - attempt.sent_at)) * 1000)
    FILTER (WHERE attempt.sent_at IS NOT NULL AND attempt.first_byte_at IS NOT NULL))::bigint, 0)::bigint AS first_byte_p95_ms,
  COALESCE((percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM (attempt.completed_at - attempt.sent_at)) * 1000)
    FILTER (WHERE attempt.sent_at IS NOT NULL AND attempt.completed_at IS NOT NULL))::bigint, 0)::bigint AS total_p95_ms
FROM request_attempts attempt JOIN requests request ON request.id = attempt.request_id
WHERE attempt.created_at >= sqlc.arg(since) AND attempt.created_at < sqlc.arg(until)
  AND (sqlc.narg(user_id)::uuid IS NULL OR request.user_id = sqlc.narg(user_id));

-- name: ListRequestTrend :many
WITH buckets AS (
  SELECT generate_series(sqlc.arg(since)::timestamptz, sqlc.arg(until)::timestamptz - interval '1 hour', interval '1 hour') AS bucket
), facts AS (
  SELECT date_trunc('hour', accepted_at) AS bucket, count(*) AS request_count,
         COALESCE(sum(input_tokens) FILTER (WHERE input_tokens IS NOT NULL), 0)::bigint AS input_tokens,
         COALESCE(sum(output_tokens) FILTER (WHERE output_tokens IS NOT NULL), 0)::bigint AS output_tokens
  FROM requests
  WHERE accepted_at >= sqlc.arg(since) AND accepted_at < sqlc.arg(until)
    AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id))
  GROUP BY date_trunc('hour', accepted_at)
)
SELECT buckets.bucket::timestamptz AS bucket,
       COALESCE(facts.request_count, 0)::bigint AS request_count,
       COALESCE(facts.input_tokens, 0)::bigint AS input_tokens,
       COALESCE(facts.output_tokens, 0)::bigint AS output_tokens
FROM buckets LEFT JOIN facts USING (bucket) ORDER BY buckets.bucket;

-- name: ListRequestErrors :many
SELECT COALESCE(error_kind, 'unknown') AS error_kind, count(*) AS request_count
FROM requests
WHERE accepted_at >= sqlc.arg(since) AND accepted_at < sqlc.arg(until)
  AND status IN ('failed', 'canceled', 'uncertain')
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id))
GROUP BY COALESCE(error_kind, 'unknown')
ORDER BY request_count DESC, error_kind LIMIT 8;
