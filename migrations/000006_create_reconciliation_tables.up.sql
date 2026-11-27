CREATE TABLE IF NOT EXISTS reconciliation_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    bank_code TEXT NOT NULL,
    currency CHAR(3) NOT NULL CHECK (currency IN ('VND', 'USD')),
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('completed', 'has_discrepancies')),
    total_bank_records INT NOT NULL DEFAULT 0,
    total_internal_records INT NOT NULL DEFAULT 0,
    matched_count INT NOT NULL DEFAULT 0,
    discrepancy_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (period_end >= period_start)
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_runs_user_id
    ON reconciliation_runs(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS reconciliation_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES reconciliation_runs(id) ON DELETE CASCADE,
    external_ref TEXT NOT NULL DEFAULT '',
    payment_id UUID REFERENCES payments(id),
    match_status TEXT NOT NULL CHECK (match_status IN (
        'matched',
        'amount_mismatch',
        'direction_mismatch',
        'status_mismatch',
        'missing_in_fincore',
        'missing_in_bank'
    )),
    bank_amount BIGINT,
    internal_amount BIGINT,
    bank_direction TEXT NOT NULL DEFAULT '',
    internal_direction TEXT NOT NULL DEFAULT '',
    bank_status TEXT NOT NULL DEFAULT '',
    internal_status TEXT NOT NULL DEFAULT '',
    currency CHAR(3) NOT NULL CHECK (currency IN ('VND', 'USD')),
    resolution_status TEXT NOT NULL DEFAULT 'open' CHECK (resolution_status IN (
        'not_applicable',
        'open',
        'resolved',
        'ignored'
    )),
    resolution_note TEXT NOT NULL DEFAULT '',
    resolved_by UUID REFERENCES users(id),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_items_run_id
    ON reconciliation_items(run_id, match_status);

CREATE TABLE IF NOT EXISTS ledger_audit_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    status TEXT NOT NULL CHECK (status IN ('healthy', 'anomalies_found')),
    checked_transactions INT NOT NULL DEFAULT 0,
    unbalanced_transactions INT NOT NULL DEFAULT 0,
    checked_accounts INT NOT NULL DEFAULT 0,
    drifted_accounts INT NOT NULL DEFAULT 0,
    anomalies JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ledger_audit_runs_user_id
    ON ledger_audit_runs(user_id, created_at DESC);
