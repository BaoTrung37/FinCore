-- name: CreateTransfer :one
INSERT INTO transfers (
    user_id,
    idempotency_key,
    request_hash,
    source_account_id,
    destination_account_id,
    amount,
    currency,
    status,
    ledger_transaction_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, user_id, idempotency_key, request_hash, source_account_id, destination_account_id, amount, currency, status, ledger_transaction_id, created_at;

-- name: GetTransferByID :one
SELECT id, user_id, idempotency_key, request_hash, source_account_id, destination_account_id, amount, currency, status, ledger_transaction_id, created_at
FROM transfers
WHERE id = $1;

-- name: GetTransferByIdempotencyKey :one
SELECT id, user_id, idempotency_key, request_hash, source_account_id, destination_account_id, amount, currency, status, ledger_transaction_id, created_at
FROM transfers
WHERE user_id = $1 AND idempotency_key = $2;
