-- name: CreateLedgerTransaction :one
INSERT INTO ledger_transactions (reference, transaction_type, reversal_of_id)
VALUES ($1, $2, $3)
RETURNING id, reference, transaction_type, status, reversal_of_id, created_at;

-- name: GetLedgerTransactionByID :one
SELECT id, reference, transaction_type, status, reversal_of_id, created_at
FROM ledger_transactions
WHERE id = $1;

-- name: MarkLedgerTransactionReversed :execrows
UPDATE ledger_transactions
SET status = 'reversed'
WHERE id = $1 AND status = 'posted';

-- name: CreateLedgerEntry :one
INSERT INTO ledger_entries (transaction_id, account_id, entry_type, amount, currency)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, transaction_id, account_id, entry_type, amount, currency, created_at;

-- name: ListEntriesByTransactionID :many
SELECT id, transaction_id, account_id, entry_type, amount, currency, created_at
FROM ledger_entries
WHERE transaction_id = $1
ORDER BY created_at ASC;

-- name: ListEntriesByAccountID :many
SELECT id, transaction_id, account_id, entry_type, amount, currency, created_at
FROM ledger_entries
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
