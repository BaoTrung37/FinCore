-- name: CreateWebhookEndpoint :one
INSERT INTO webhook_endpoints (user_id, url, secret, events)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListWebhookEndpointsByUser :many
SELECT *
FROM webhook_endpoints
WHERE user_id = $1 AND is_active = TRUE
ORDER BY created_at DESC;

-- name: DeactivateWebhookEndpoint :execrows
UPDATE webhook_endpoints
SET is_active = FALSE, updated_at = now()
WHERE id = $1 AND user_id = $2 AND is_active = TRUE;

-- name: ListActiveWebhooksForEvent :many
SELECT *
FROM webhook_endpoints
WHERE user_id = $1
  AND is_active = TRUE
  AND ($2::text = ANY(events) OR cardinality(events) = 0);

-- name: InsertOutboxEvent :one
INSERT INTO outbox_events (user_id, aggregate_type, aggregate_id, event_type, payload, max_attempts)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ClaimPendingOutboxEvents :many
SELECT *
FROM outbox_events
WHERE status = 'pending'
  AND next_retry_at <= now()
ORDER BY created_at ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxEventCompleted :exec
UPDATE outbox_events
SET status = 'completed',
    processed_at = now(),
    last_error = ''
WHERE id = $1;

-- name: MarkOutboxEventRetry :exec
UPDATE outbox_events
SET attempts = attempts + 1,
    next_retry_at = $2,
    last_error = $3
WHERE id = $1;

-- name: MarkOutboxEventFailed :exec
UPDATE outbox_events
SET status = 'failed',
    attempts = attempts + 1,
    last_error = $2,
    processed_at = now()
WHERE id = $1;
