-- name: ClaimServicePlanMutation :one
INSERT INTO service_plan_mutations (actor_user_id, action, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, action, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetServicePlanMutation :one
SELECT * FROM service_plan_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND action = sqlc.arg(action) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteServicePlanMutation :one
UPDATE service_plan_mutations SET service_plan_id = sqlc.arg(service_plan_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: CreateServicePlan :one
INSERT INTO service_plans (slug, name, description, kind, created_by)
VALUES (sqlc.arg(slug), sqlc.arg(name), sqlc.arg(description), sqlc.arg(kind), sqlc.arg(created_by))
RETURNING *;

-- name: GetServicePlanForUpdate :one
SELECT * FROM service_plans WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: NextServicePlanVersion :one
SELECT (coalesce(max(version), 0) + 1)::integer AS version
FROM service_plan_versions WHERE service_plan_id = sqlc.arg(service_plan_id);

-- name: CreateServicePlanVersion :one
INSERT INTO service_plan_versions (
  service_plan_id, version, token_quota, validity_days, concurrency_limit, rpm_limit, tpm_limit, created_by
) VALUES (
  sqlc.arg(service_plan_id), sqlc.arg(version), sqlc.arg(token_quota), sqlc.arg(validity_days),
  sqlc.arg(concurrency_limit), sqlc.narg(rpm_limit), sqlc.narg(tpm_limit), sqlc.arg(created_by)
) RETURNING *;

-- name: CreateServicePlanVersionRoute :exec
INSERT INTO service_plan_version_routes (service_plan_version_id, model_id, resource_pool_id)
VALUES (sqlc.arg(service_plan_version_id), sqlc.arg(model_id), sqlc.arg(resource_pool_id));

-- name: PublishServicePlanVersion :one
UPDATE service_plans
SET name = sqlc.arg(name), description = sqlc.arg(description), kind = sqlc.arg(kind),
    status = 'active', current_version_id = sqlc.arg(current_version_id), updated_at = now()
WHERE id = sqlc.arg(id) RETURNING *;

-- name: SetServicePlanStatus :one
UPDATE service_plans SET status = sqlc.arg(status), updated_at = now()
WHERE id = sqlc.arg(id) AND status <> 'archived' RETURNING *;

-- name: GetServicePlan :one
SELECT plan.*, version.version, version.token_quota, version.validity_days,
       version.concurrency_limit, version.rpm_limit, version.tpm_limit,
       version.created_by AS version_created_by, version.created_at AS version_created_at,
       (SELECT count(*) FROM service_plan_version_routes route WHERE route.service_plan_version_id = version.id) AS route_count
FROM service_plans plan
LEFT JOIN service_plan_versions version ON version.id = plan.current_version_id
WHERE plan.id = sqlc.arg(id);

-- name: ListServicePlans :many
SELECT plan.*, version.version, version.token_quota, version.validity_days,
       version.concurrency_limit, version.rpm_limit, version.tpm_limit,
       version.created_by AS version_created_by, version.created_at AS version_created_at,
       (SELECT count(*) FROM service_plan_version_routes route WHERE route.service_plan_version_id = version.id) AS route_count,
       (SELECT count(*) FROM subscriptions subscription
        WHERE subscription.service_plan_version_id = version.id
          AND subscription.status IN ('scheduled', 'active')
          AND subscription.expires_at > now()) AS active_subscription_count
FROM service_plans plan
LEFT JOIN service_plan_versions version ON version.id = plan.current_version_id
WHERE (sqlc.arg(include_archived)::boolean OR plan.status <> 'archived')
ORDER BY CASE plan.status WHEN 'active' THEN 0 WHEN 'disabled' THEN 1 ELSE 2 END, plan.name, plan.id;

-- name: ListServicePlanVersionRoutes :many
SELECT route.service_plan_version_id, route.model_id, model.public_name AS model_name,
       route.resource_pool_id, pool.name AS resource_pool_name, pool.slug AS resource_pool_slug,
       provider.name AS provider_name
FROM service_plan_version_routes route
JOIN models model ON model.id = route.model_id
JOIN resource_pools pool ON pool.id = route.resource_pool_id
JOIN providers provider ON provider.id = pool.provider_id
WHERE route.service_plan_version_id = sqlc.arg(service_plan_version_id)
ORDER BY model.public_name, pool.name, route.model_id;

-- name: GetCurrentServicePlanVersion :one
SELECT version.* FROM service_plans plan
JOIN service_plan_versions version ON version.id = plan.current_version_id
WHERE plan.id = sqlc.arg(service_plan_id) AND plan.status = 'active';

-- name: ClaimSubscriptionMutation :one
INSERT INTO subscription_mutations (actor_user_id, action, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, action, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetSubscriptionMutation :one
SELECT * FROM subscription_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND action = sqlc.arg(action) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteSubscriptionMutation :one
UPDATE subscription_mutations SET subscription_id = sqlc.arg(subscription_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: CreateSubscription :one
INSERT INTO subscriptions (
  user_id, service_plan_version_id, status, granted_tokens, starts_at, expires_at, notes, assigned_by
) VALUES (
  sqlc.arg(user_id), sqlc.arg(service_plan_version_id), sqlc.arg(status), sqlc.arg(granted_tokens),
  sqlc.arg(starts_at), sqlc.arg(expires_at), sqlc.arg(notes), sqlc.arg(assigned_by)
) RETURNING *;

-- name: GetSubscriptionForUpdate :one
SELECT * FROM subscriptions WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateSubscriptionStatus :one
UPDATE subscriptions
SET status = sqlc.arg(status),
    suspended_at = CASE WHEN sqlc.arg(status)::subscription_status = 'suspended' THEN now() ELSE NULL END,
    canceled_at = CASE WHEN sqlc.arg(status)::subscription_status = 'canceled' THEN now() ELSE NULL END,
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'canceled' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: UpdateSubscriptionTerm :one
UPDATE subscriptions
SET granted_tokens = sqlc.arg(granted_tokens), starts_at = sqlc.arg(starts_at), expires_at = sqlc.arg(expires_at),
    notes = sqlc.arg(notes),
    status = sqlc.arg(status), suspended_at = NULL, canceled_at = NULL,
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'canceled' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: GetSubscription :one
SELECT subscription.*, plan.id AS service_plan_id, plan.name AS service_plan_name, plan.kind AS plan_kind,
       version.version AS plan_version, version.concurrency_limit, version.rpm_limit, version.tpm_limit,
       member.email AS member_email, member.display_name AS member_name,
       coalesce((SELECT sum(event.token_delta) FROM ledger_events event WHERE event.subscription_id = subscription.id), 0)::bigint AS balance_tokens
FROM subscriptions subscription
JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
JOIN service_plans plan ON plan.id = version.service_plan_id
JOIN users member ON member.id = subscription.user_id
WHERE subscription.id = sqlc.arg(id);

-- name: ListSubscriptions :many
SELECT subscription.*, plan.id AS service_plan_id, plan.name AS service_plan_name, plan.kind AS plan_kind,
       version.version AS plan_version, version.concurrency_limit, version.rpm_limit, version.tpm_limit,
       member.email AS member_email, member.display_name AS member_name,
       coalesce((SELECT sum(event.token_delta) FROM ledger_events event WHERE event.subscription_id = subscription.id), 0)::bigint AS balance_tokens
FROM subscriptions subscription
JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
JOIN service_plans plan ON plan.id = version.service_plan_id
JOIN users member ON member.id = subscription.user_id
WHERE (sqlc.narg(user_id)::uuid IS NULL OR subscription.user_id = sqlc.narg(user_id))
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', member.email, member.display_name, plan.name) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(status)::text = '' OR
       CASE
         WHEN subscription.status IN ('suspended', 'canceled') THEN subscription.status::text
         WHEN subscription.expires_at <= now() THEN 'expired'
         WHEN subscription.starts_at > now() THEN 'scheduled'
         ELSE 'active'
       END = sqlc.arg(status)::text)
ORDER BY subscription.created_at DESC, subscription.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountSubscriptions :one
SELECT count(*)
FROM subscriptions subscription
JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
JOIN service_plans plan ON plan.id = version.service_plan_id
JOIN users member ON member.id = subscription.user_id
WHERE (sqlc.narg(user_id)::uuid IS NULL OR subscription.user_id = sqlc.narg(user_id))
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', member.email, member.display_name, plan.name) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(status)::text = '' OR
       CASE
         WHEN subscription.status IN ('suspended', 'canceled') THEN subscription.status::text
         WHEN subscription.expires_at <= now() THEN 'expired'
         WHEN subscription.starts_at > now() THEN 'scheduled'
         ELSE 'active'
       END = sqlc.arg(status)::text);

-- name: GetApplicableSubscriptionRoutesForUpdate :many
SELECT subscription.*, version.concurrency_limit, version.rpm_limit, version.tpm_limit,
       route.resource_pool_id
FROM subscriptions subscription
JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
JOIN service_plan_version_routes route ON route.service_plan_version_id = version.id
JOIN resource_pools pool ON pool.id = route.resource_pool_id
WHERE subscription.user_id = sqlc.arg(user_id) AND route.model_id = sqlc.arg(model_id)
  AND subscription.status IN ('scheduled', 'active')
  AND subscription.starts_at <= now() AND subscription.expires_at > now()
  AND pool.status = 'active'
ORDER BY subscription.expires_at, subscription.created_at, subscription.id
FOR UPDATE OF subscription;
