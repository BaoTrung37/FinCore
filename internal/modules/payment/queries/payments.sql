-- name: CreatePayment :one
INSERT INTO payments (
    user_id,
    account_id,
    idempotency_key,
    request_hash,
    direction,
    amount,
    currency,
    bank_code,
    bank_account_number,
    external_ref,
    status,
    failure_reason,
    ledger_transaction_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING id, user_id, account_id, idempotency_key, request_hash, direction, amount, currency, bank_code, bank_account_number, external_ref, status, failure_reason, ledger_transaction_id, created_at, updated_at;

-- name: GetPaymentByID :one
SELECT id, user_id, account_id, idempotency_key, request_hash, direction, amount, currency, bank_code, bank_account_number, external_ref, status, failure_reason, ledger_transaction_id, created_at, updated_at
FROM payments
WHERE id = $1;

-- name: GetPaymentByIDForUpdate :one
SELECT id, user_id, account_id, idempotency_key, request_hash, direction, amount, currency, bank_code, bank_account_number, external_ref, status, failure_reason, ledger_transaction_id, created_at, updated_at
FROM payments
WHERE id = $1
FOR UPDATE;

-- name: GetPaymentByExternalRefForUpdate :one
SELECT id, user_id, account_id, idempotency_key, request_hash, direction, amount, currency, bank_code, bank_account_number, external_ref, status, failure_reason, ledger_transaction_id, created_at, updated_at
FROM payments
WHERE external_ref = $1::text
FOR UPDATE;

-- name: GetPaymentByIdempotencyKey :one
SELECT id, user_id, account_id, idempotency_key, request_hash, direction, amount, currency, bank_code, bank_account_number, external_ref, status, failure_reason, ledger_transaction_id, created_at, updated_at
FROM payments
WHERE user_id = $1 AND idempotency_key = $2;

-- name: ListPaymentsByUserID :many
SELECT id, user_id, account_id, idempotency_key, request_hash, direction, amount, currency, bank_code, bank_account_number, external_ref, status, failure_reason, ledger_transaction_id, created_at, updated_at
FROM payments
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdatePaymentStatus :one
UPDATE payments
SET status = $2,
    failure_reason = $3,
    ledger_transaction_id = $4,
    updated_at = now()
WHERE id = $1
RETURNING id, user_id, account_id, idempotency_key, request_hash, direction, amount, currency, bank_code, bank_account_number, external_ref, status, failure_reason, ledger_transaction_id, created_at, updated_at;
