-- name: CreateAccount :one
INSERT INTO accounts (user_id, account_number, currency)
VALUES ($1, $2, $3)
RETURNING id, user_id, account_number, currency, status, balance_amount, created_at, updated_at;

-- name: ListAccountsByUserID :many
SELECT id, user_id, account_number, currency, status, balance_amount, created_at, updated_at
FROM accounts
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: GetAccountByID :one
SELECT id, user_id, account_number, currency, status, balance_amount, created_at, updated_at
FROM accounts
WHERE id = $1;

-- name: GetAccountsForUpdate :many
SELECT id, user_id, account_number, currency, status, balance_amount, created_at, updated_at
FROM accounts
WHERE id = ANY($1::uuid[])
ORDER BY id ASC
FOR UPDATE;

-- name: UpdateAccountStatus :one
UPDATE accounts
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING id, user_id, account_number, currency, status, balance_amount, created_at, updated_at;

-- name: UpdateAccountBalance :one
UPDATE accounts
SET balance_amount = balance_amount + $2, updated_at = now()
WHERE id = $1
RETURNING id, user_id, account_number, currency, status, balance_amount, created_at, updated_at;
