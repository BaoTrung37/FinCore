-- name: CreateReconciliationRun :one
INSERT INTO reconciliation_runs (
    user_id,
    bank_code,
    currency,
    period_start,
    period_end,
    status,
    total_bank_records,
    total_internal_records,
    matched_count,
    discrepancy_count
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, user_id, bank_code, currency, period_start, period_end, status, total_bank_records, total_internal_records, matched_count, discrepancy_count, created_at;

-- name: GetReconciliationRunByIDAndUser :one
SELECT id, user_id, bank_code, currency, period_start, period_end, status, total_bank_records, total_internal_records, matched_count, discrepancy_count, created_at
FROM reconciliation_runs
WHERE id = $1 AND user_id = $2;

-- name: ListReconciliationRunsByUser :many
SELECT id, user_id, bank_code, currency, period_start, period_end, status, total_bank_records, total_internal_records, matched_count, discrepancy_count, created_at
FROM reconciliation_runs
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateReconciliationItem :one
INSERT INTO reconciliation_items (
    run_id,
    external_ref,
    payment_id,
    match_status,
    bank_amount,
    internal_amount,
    bank_direction,
    internal_direction,
    bank_status,
    internal_status,
    currency,
    resolution_status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id, run_id, external_ref, payment_id, match_status, bank_amount, internal_amount, bank_direction, internal_direction, bank_status, internal_status, currency, resolution_status, resolution_note, resolved_by, resolved_at, created_at;

-- name: ListReconciliationItemsByRun :many
SELECT id, run_id, external_ref, payment_id, match_status, bank_amount, internal_amount, bank_direction, internal_direction, bank_status, internal_status, currency, resolution_status, resolution_note, resolved_by, resolved_at, created_at
FROM reconciliation_items
WHERE run_id = $1
  AND (sqlc.narg('match_status')::text IS NULL OR match_status = sqlc.narg('match_status'))
  AND (sqlc.narg('resolution_status')::text IS NULL OR resolution_status = sqlc.narg('resolution_status'))
ORDER BY created_at ASC, id ASC;

-- name: GetReconciliationItemByIDAndRun :one
SELECT id, run_id, external_ref, payment_id, match_status, bank_amount, internal_amount, bank_direction, internal_direction, bank_status, internal_status, currency, resolution_status, resolution_note, resolved_by, resolved_at, created_at
FROM reconciliation_items
WHERE id = $1 AND run_id = $2;

-- name: ResolveReconciliationItem :one
UPDATE reconciliation_items
SET resolution_status = $3,
    resolution_note = $4,
    resolved_by = $5,
    resolved_at = now()
WHERE id = $1 AND run_id = $2
RETURNING id, run_id, external_ref, payment_id, match_status, bank_amount, internal_amount, bank_direction, internal_direction, bank_status, internal_status, currency, resolution_status, resolution_note, resolved_by, resolved_at, created_at;

-- name: ListPaymentsForReconciliation :many
SELECT
    id,
    COALESCE(external_ref, '')::text AS external_ref,
    direction,
    amount,
    currency,
    status
FROM payments
WHERE user_id = $1
  AND bank_code = $2
  AND currency = $3
  AND external_ref IS NOT NULL
  AND external_ref != ''
  AND created_at >= $4
  AND created_at <= $5
ORDER BY created_at ASC;

-- name: CountLedgerTransactionsByUser :one
SELECT COUNT(DISTINCT lt.id)::bigint
FROM ledger_transactions lt
JOIN ledger_entries le ON le.transaction_id = lt.id
JOIN accounts a ON a.id = le.account_id
WHERE a.user_id = $1;

-- name: FindUnbalancedLedgerTransactionsByUser :many
SELECT
    lt.id AS transaction_id,
    lt.reference,
    COUNT(le.id)::bigint AS entry_count,
    COUNT(DISTINCT le.currency)::bigint AS currency_count,
    COALESCE(SUM(CASE WHEN le.entry_type = 'debit' THEN le.amount ELSE 0 END), 0)::bigint AS total_debit,
    COALESCE(SUM(CASE WHEN le.entry_type = 'credit' THEN le.amount ELSE 0 END), 0)::bigint AS total_credit
FROM ledger_transactions lt
LEFT JOIN ledger_entries le ON le.transaction_id = lt.id
WHERE EXISTS (
    SELECT 1
    FROM ledger_entries le2
    JOIN accounts a ON a.id = le2.account_id
    WHERE le2.transaction_id = lt.id
      AND a.user_id = $1
)
GROUP BY lt.id, lt.reference
HAVING COUNT(le.id) < 2
    OR COUNT(DISTINCT le.currency) > 1
    OR COALESCE(SUM(CASE WHEN le.entry_type = 'debit' THEN le.amount ELSE 0 END), 0)
       != COALESCE(SUM(CASE WHEN le.entry_type = 'credit' THEN le.amount ELSE 0 END), 0)
ORDER BY lt.created_at ASC;

-- name: CountAccountsByUser :one
SELECT COUNT(*)::bigint
FROM accounts
WHERE user_id = $1;

-- name: FindDriftedAccountsByUser :many
SELECT
    a.id AS account_id,
    a.account_number,
    a.currency,
    a.balance_amount AS cached_balance,
    COALESCE(SUM(CASE WHEN le.entry_type = 'credit' THEN le.amount ELSE -le.amount END), 0)::bigint AS computed_balance
FROM accounts a
LEFT JOIN ledger_entries le ON le.account_id = a.id
WHERE a.user_id = $1
GROUP BY a.id, a.account_number, a.currency, a.balance_amount
HAVING a.balance_amount != COALESCE(SUM(CASE WHEN le.entry_type = 'credit' THEN le.amount ELSE -le.amount END), 0)
ORDER BY a.created_at ASC;

-- name: CreateLedgerAuditRun :one
INSERT INTO ledger_audit_runs (
    user_id,
    status,
    checked_transactions,
    unbalanced_transactions,
    checked_accounts,
    drifted_accounts,
    anomalies
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, user_id, status, checked_transactions, unbalanced_transactions, checked_accounts, drifted_accounts, anomalies, created_at;

-- name: ListLedgerAuditRunsByUser :many
SELECT id, user_id, status, checked_transactions, unbalanced_transactions, checked_accounts, drifted_accounts, anomalies, created_at
FROM ledger_audit_runs
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
