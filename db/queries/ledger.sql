-- name: CreateEntitlement :one
INSERT INTO entitlements (user_id, plan, resource_domain, model_id, granted_tokens, starts_at, expires_at, concurrency_limit, rpm_limit, tpm_limit)
VALUES (sqlc.arg(user_id), sqlc.arg(plan), sqlc.arg(resource_domain), sqlc.narg(model_id), sqlc.arg(granted_tokens), sqlc.arg(starts_at), sqlc.arg(expires_at), sqlc.arg(concurrency_limit), sqlc.narg(rpm_limit), sqlc.narg(tpm_limit))
RETURNING *;

-- name: GetEntitlementForUpdate :one
SELECT * FROM entitlements WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: GetEntitlementByGrantIdempotency :one
SELECT e.*, le.note AS grant_note
FROM ledger_events le
JOIN entitlements e ON e.id = le.entitlement_id
WHERE le.source_event_id = sqlc.arg(source_event_id)
  AND le.kind = 'grant'
  AND le.created_by = sqlc.arg(created_by);

-- name: ListActiveEntitlements :many
SELECT * FROM entitlements WHERE user_id = sqlc.arg(user_id) AND starts_at <= now() AND expires_at > now() ORDER BY resource_domain, expires_at, id;

-- name: ListEntitlementsWithBalance :many
SELECT e.*, coalesce(sum(le.token_delta), 0)::bigint AS balance_tokens
FROM entitlements e
LEFT JOIN ledger_events le ON le.entitlement_id = e.id
WHERE (sqlc.narg(user_id)::uuid IS NULL OR e.user_id = sqlc.narg(user_id))
GROUP BY e.id
ORDER BY e.created_at DESC, e.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: ListUserModelAuthorizations :many
SELECT a.user_id, a.model_id, a.created_at, m.public_name, m.display_name, m.resource_domain, m.enabled
FROM model_authorizations a
JOIN models m ON m.id = a.model_id
WHERE a.user_id = sqlc.arg(user_id)
ORDER BY m.public_name, m.id;

-- name: GetAuthorizedModelDomain :one
SELECT m.resource_domain
FROM models m
JOIN model_authorizations a ON a.model_id = m.id AND a.user_id = sqlc.arg(user_id)
WHERE m.id = sqlc.arg(model_id) AND m.enabled = true;

-- name: GetModelDomain :one
SELECT resource_domain FROM models WHERE id = sqlc.arg(model_id);

-- name: IsGatewayKeyOwnedByUser :one
SELECT EXISTS (
  SELECT 1 FROM gateway_keys
  WHERE id = sqlc.arg(gateway_key_id)
    AND user_id = sqlc.arg(user_id)
    AND revoked_at IS NULL
    AND (expires_at IS NULL OR expires_at > now())
);

-- name: ListApplicableEntitlementsForUpdate :many
SELECT * FROM entitlements
WHERE user_id = sqlc.arg(user_id)
  AND resource_domain = sqlc.arg(resource_domain)
  AND starts_at <= now() AND expires_at > now()
  AND (model_id IS NULL OR model_id = sqlc.arg(model_id))
ORDER BY (model_id IS NOT NULL) DESC, expires_at, id
FOR UPDATE;

-- name: CreateLedgerEvent :one
INSERT INTO ledger_events (user_id, entitlement_id, request_id, reservation_id, kind, token_delta, reserved_tokens, input_tokens, output_tokens, usage_source, source_event_id, note, created_by)
VALUES (sqlc.arg(user_id), sqlc.arg(entitlement_id), sqlc.narg(request_id), sqlc.narg(reservation_id), sqlc.arg(kind), sqlc.arg(token_delta), sqlc.arg(reserved_tokens), sqlc.arg(input_tokens), sqlc.arg(output_tokens), sqlc.arg(usage_source), sqlc.narg(source_event_id), sqlc.narg(note), sqlc.narg(created_by))
RETURNING *;

-- name: EntitlementBalance :one
SELECT coalesce(sum(token_delta), 0)::bigint FROM ledger_events WHERE entitlement_id = sqlc.arg(entitlement_id);

-- name: ListLedgerEventsByEntitlement :many
SELECT * FROM ledger_events WHERE entitlement_id = sqlc.arg(entitlement_id) ORDER BY created_at, id;

-- name: CreateLedgerReservation :one
INSERT INTO ledger_reservations (id, entitlement_id, request_id, state, reserved_tokens, reserve_event_id)
VALUES (sqlc.arg(id), sqlc.arg(entitlement_id), sqlc.arg(request_id), 'reserved', sqlc.arg(reserved_tokens), sqlc.arg(reserve_event_id))
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

-- name: ListLedgerEvents :many
SELECT * FROM ledger_events
WHERE (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id))
  AND (sqlc.narg(entitlement_id)::uuid IS NULL OR entitlement_id = sqlc.narg(entitlement_id))
ORDER BY created_at DESC, id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CreateRequest :one
INSERT INTO requests (idempotency_key, request_digest, user_id, gateway_key_id, model_id, entitlement_id, config_revision_id, resource_domain, status, stream)
VALUES (sqlc.narg(idempotency_key), sqlc.arg(request_digest), sqlc.arg(user_id), sqlc.arg(gateway_key_id), sqlc.arg(model_id), sqlc.arg(entitlement_id), sqlc.narg(config_revision_id), sqlc.arg(resource_domain), sqlc.arg(status), sqlc.arg(stream))
RETURNING *;

-- name: GetRequestByIdempotencyKey :one
SELECT * FROM requests WHERE gateway_key_id = sqlc.arg(gateway_key_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: GetRequest :one
SELECT * FROM requests WHERE id = sqlc.arg(id);

-- name: GetRequestForUpdate :one
SELECT * FROM requests WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateRequestStatus :one
UPDATE requests SET status = sqlc.arg(status), error_kind = sqlc.narg(error_kind), error_detail = sqlc.narg(error_detail), updated_at = now(), completed_at = CASE WHEN sqlc.arg(status)::request_status IN ('completed','failed','canceled','uncertain') THEN now() ELSE completed_at END
WHERE id = sqlc.arg(id) RETURNING *;

-- name: CompleteRequest :one
UPDATE requests SET status = 'completed', input_tokens = sqlc.arg(input_tokens), output_tokens = sqlc.arg(output_tokens), usage_source = sqlc.arg(usage_source), error_kind = NULL, error_detail = NULL, completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) RETURNING *;

-- name: FailRequestWithUsage :one
UPDATE requests
SET status = 'failed', input_tokens = sqlc.arg(input_tokens), output_tokens = sqlc.arg(output_tokens), usage_source = sqlc.arg(usage_source),
    error_kind = sqlc.arg(error_kind), error_detail = sqlc.narg(error_detail), completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListRequests :many
SELECT * FROM requests
WHERE (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id))
ORDER BY accepted_at DESC, id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CreateAttempt :one
INSERT INTO request_attempts (request_id, credential_id, sequence, status)
VALUES (sqlc.arg(request_id), sqlc.arg(credential_id), sqlc.arg(sequence), sqlc.arg(status)) RETURNING *;

-- name: UpdateAttempt :one
UPDATE request_attempts SET status = sqlc.arg(status), upstream_request_id = sqlc.narg(upstream_request_id), http_status = sqlc.narg(http_status), error_kind = sqlc.narg(error_kind), retry_after_at = sqlc.narg(retry_after_at), sent_at = coalesce(sent_at, sqlc.narg(sent_at)), first_byte_at = coalesce(first_byte_at, sqlc.narg(first_byte_at)), completed_at = sqlc.narg(completed_at)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: ListRequestAttempts :many
SELECT * FROM request_attempts WHERE request_id = sqlc.arg(request_id) ORDER BY sequence, id;

-- name: CreateResponseRecord :one
INSERT INTO response_records (request_id, status) VALUES (sqlc.arg(request_id), sqlc.arg(status)) RETURNING *;

-- name: GetResponseRecord :one
SELECT * FROM response_records WHERE id = sqlc.arg(id);

-- name: UpdateResponseRecord :one
UPDATE response_records SET status = sqlc.arg(status), output = sqlc.narg(output), error = sqlc.narg(error), updated_at = now() WHERE id = sqlc.arg(id) RETURNING *;

-- name: RequestResponseCancellation :execrows
UPDATE response_records SET cancel_requested_at = now(), updated_at = now() WHERE id = sqlc.arg(id) AND status IN ('queued','dispatching','streaming');
