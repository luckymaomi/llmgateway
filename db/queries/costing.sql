-- name: ClaimModelPriceMutation :one
INSERT INTO model_price_mutations (actor_user_id, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetModelPriceMutation :one
SELECT * FROM model_price_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteModelPriceMutation :one
UPDATE model_price_mutations SET price_version_id = sqlc.arg(price_version_id)
WHERE id = sqlc.arg(id) AND price_version_id IS NULL
RETURNING *;

-- name: CreateModelPriceVersion :one
INSERT INTO model_price_versions (model_id, currency, input_rate_nanos_per_million, output_rate_nanos_per_million, effective_at, created_by)
VALUES (sqlc.arg(model_id), sqlc.arg(currency), sqlc.arg(input_rate_nanos_per_million), sqlc.arg(output_rate_nanos_per_million), sqlc.arg(effective_at), sqlc.arg(created_by))
RETURNING *;

-- name: GetModelPriceVersion :one
SELECT price.*, model.public_name AS model_alias
FROM model_price_versions price JOIN models model ON model.id = price.model_id
WHERE price.id = sqlc.arg(id);

-- name: GetEffectiveModelPrice :one
SELECT * FROM model_price_versions
WHERE model_id = sqlc.arg(model_id) AND effective_at <= now()
ORDER BY effective_at DESC, created_at DESC, id DESC
LIMIT 1;

-- name: ListModelPriceVersions :many
SELECT price.*, model.public_name AS model_alias
FROM model_price_versions price JOIN models model ON model.id = price.model_id
WHERE (sqlc.narg(model_id)::uuid IS NULL OR price.model_id = sqlc.narg(model_id))
ORDER BY price.effective_at DESC, price.created_at DESC, price.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: ListCostSummaries :many
SELECT request.user_id, user_record.display_name AS user_name,
       request.subscription_id, plan.name AS service_plan_name, plan.kind::text AS plan_kind,
       request.model_id, model.public_name AS model_alias,
       model.provider_id, provider.name AS provider_name,
       request.resource_pool_id, pool.name AS resource_pool_name, request.cost_currency AS currency,
       count(*)::bigint AS request_count,
       sum(request.input_tokens)::bigint AS input_tokens,
       sum(request.output_tokens)::bigint AS output_tokens,
       sum(request.input_cost_nanos)::bigint AS input_cost_nanos,
       sum(request.output_cost_nanos)::bigint AS output_cost_nanos,
       sum(request.total_cost_nanos)::bigint AS total_cost_nanos
FROM requests request
JOIN users user_record ON user_record.id = request.user_id
JOIN subscriptions subscription ON subscription.id = request.subscription_id
JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
JOIN service_plans plan ON plan.id = version.service_plan_id
JOIN models model ON model.id = request.model_id
JOIN providers provider ON provider.id = model.provider_id
JOIN resource_pools pool ON pool.id = request.resource_pool_id
WHERE request.total_cost_nanos IS NOT NULL
GROUP BY request.user_id, user_record.display_name, request.subscription_id, plan.name, plan.kind,
         request.model_id, model.public_name, model.provider_id, provider.name,
         request.resource_pool_id, pool.name, request.cost_currency
ORDER BY max(request.completed_at) DESC NULLS LAST, request.user_id, request.subscription_id, request.model_id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);
