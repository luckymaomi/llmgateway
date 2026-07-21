-- name: CreateResponseRecord :one
INSERT INTO response_records (
    id, request_id, gateway_key_id, previous_response_id, status, background, encrypted_input
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: CreateBackgroundResponseRecord :one
INSERT INTO response_records (
    id, gateway_key_id, previous_response_id, idempotency_key, request_digest,
    status, background, encrypted_input, encrypted_request
) VALUES (
    $1, $2, $3, $4, $5, 'queued', true, $6, $7
)
ON CONFLICT (gateway_key_id, idempotency_key) WHERE idempotency_key IS NOT NULL
DO UPDATE SET updated_at = response_records.updated_at
WHERE response_records.request_digest = EXCLUDED.request_digest
RETURNING *;

-- name: CreateCompletedResponseRecord :one
INSERT INTO response_records (
    id, request_id, gateway_key_id, previous_response_id, status, background,
    encrypted_input, encrypted_output, completed_at
) VALUES (
    $1, $2, $3, $4, 'completed', false, $5, $6, now()
)
RETURNING *;

-- name: CompleteResponseRecord :one
UPDATE response_records
SET status = 'completed', encrypted_output = $2, encrypted_error = NULL,
    completed_at = now(), updated_at = now()
WHERE id = $1 AND status = 'in_progress'
RETURNING *;

-- name: FailResponseRecord :one
UPDATE response_records
SET status = 'failed', encrypted_error = $2, completed_at = now(), updated_at = now()
WHERE id = $1 AND status = 'in_progress'
RETURNING *;

-- name: GetResponseRecord :one
SELECT *
FROM response_records
WHERE id = $1 AND gateway_key_id = $2;

-- name: DeleteResponseRecord :one
DELETE FROM response_records
WHERE id = $1 AND gateway_key_id = $2
RETURNING id;

-- name: RequestResponseCancellation :one
UPDATE response_records
SET cancel_requested_at = COALESCE(cancel_requested_at, now()),
    status = 'canceled', completed_at = COALESCE(completed_at, now()), updated_at = now()
WHERE id = $1 AND gateway_key_id = $2 AND background = true
  AND status IN ('queued', 'in_progress', 'canceled')
RETURNING *;

-- name: ClaimBackgroundResponse :one
WITH candidate AS (
    SELECT response.id
    FROM response_records response
    WHERE response.background = true
      AND response.cancel_requested_at IS NULL
      AND (
        response.status = 'queued'
        OR (
          response.status = 'in_progress'
          AND response.execution_heartbeat_at < sqlc.arg(stale_before)
          AND response.request_id IS NULL
          AND NOT EXISTS (SELECT 1 FROM requests request WHERE request.id = response.id)
        )
      )
    ORDER BY response.created_at, response.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE response_records response
SET status = 'in_progress', execution_id = sqlc.arg(execution_id),
    execution_generation = response.execution_generation + 1,
    execution_claimed_at = now(), execution_heartbeat_at = now(), updated_at = now()
FROM candidate
WHERE response.id = candidate.id
RETURNING response.*;

-- name: HeartbeatBackgroundResponse :one
UPDATE response_records
SET execution_heartbeat_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status = 'in_progress'
  AND cancel_requested_at IS NULL
RETURNING *;

-- name: StageBackgroundResponseOutput :one
UPDATE response_records
SET request_id = sqlc.arg(request_id), encrypted_output = sqlc.arg(encrypted_output), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status = 'in_progress'
  AND cancel_requested_at IS NULL
RETURNING *;

-- name: LinkBackgroundResponseRequest :one
UPDATE response_records
SET request_id = sqlc.arg(request_id), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status = 'in_progress'
  AND cancel_requested_at IS NULL
RETURNING *;

-- name: CompleteBackgroundResponse :one
UPDATE response_records
SET request_id = sqlc.arg(request_id), status = 'completed', encrypted_error = NULL,
    completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status = 'in_progress'
  AND cancel_requested_at IS NULL
  AND encrypted_output IS NOT NULL
RETURNING *;

-- name: TerminateBackgroundResponse :one
UPDATE response_records
SET request_id = CASE WHEN sqlc.narg(request_id)::uuid IS NULL THEN request_id ELSE sqlc.narg(request_id) END,
    status = sqlc.arg(status), encrypted_error = sqlc.arg(encrypted_error),
    completed_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
  AND execution_id = sqlc.arg(execution_id)
  AND execution_generation = sqlc.arg(execution_generation)
  AND status = 'in_progress'
RETURNING *;

-- name: ListBackgroundResponseRecoveries :many
SELECT response.id, response.status AS response_status, response.encrypted_output,
       request.status AS request_status, request.error_kind, request.error_detail
FROM response_records response
JOIN requests request ON request.id = response.id
WHERE response.background = true
  AND response.status IN ('queued', 'in_progress')
ORDER BY response.updated_at, response.id
LIMIT sqlc.arg(batch_size);

-- name: AttachBackgroundResponseRequest :one
UPDATE response_records
SET request_id = response_records.id, updated_at = now()
WHERE response_records.id = sqlc.arg(id) AND background = true
  AND (request_id IS NULL OR request_id = response_records.id)
  AND EXISTS (SELECT 1 FROM requests request WHERE request.id = response_records.id)
RETURNING *;

-- name: FinalizeRecoveredBackgroundResponse :one
UPDATE response_records
SET request_id = response_records.id, status = sqlc.arg(status), encrypted_error = sqlc.narg(encrypted_error),
    completed_at = now(), updated_at = now()
WHERE response_records.id = sqlc.arg(id) AND background = true AND status IN ('queued', 'in_progress')
  AND EXISTS (SELECT 1 FROM requests request WHERE request.id = response_records.id)
RETURNING *;
