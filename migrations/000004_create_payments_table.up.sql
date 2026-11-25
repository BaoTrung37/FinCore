ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS account_type TEXT NOT NULL DEFAULT 'customer'
    CHECK (account_type IN ('customer', 'settlement'));

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_balance_amount_check;

ALTER TABLE accounts
    ADD CONSTRAINT accounts_balance_amount_check
    CHECK (account_type = 'settlement' OR balance_amount >= 0);

CREATE INDEX IF NOT EXISTS idx_accounts_type_currency ON accounts(account_type, currency);

INSERT INTO users (id, email, password_hash, status)
VALUES ('00000000-0000-0000-0000-000000000001', 'settlement@fincore.internal', '!system-locked!', 'system')
ON CONFLICT (id) DO NOTHING;

INSERT INTO accounts (id, user_id, account_number, currency, account_type, status, balance_amount)
VALUES
    ('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-000000000001', 'SETTLEMENT-VND', 'VND', 'settlement', 'active', 0),
    ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-000000000001', 'SETTLEMENT-USD', 'USD', 'settlement', 'active', 0)
ON CONFLICT (account_number) DO NOTHING;

CREATE TABLE IF NOT EXISTS payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    account_id UUID NOT NULL REFERENCES accounts(id),
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('deposit', 'withdrawal')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency CHAR(3) NOT NULL CHECK (currency IN ('VND', 'USD')),
    bank_code TEXT NOT NULL,
    bank_account_number TEXT NOT NULL,
    external_ref TEXT UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'failed')),
    failure_reason TEXT NOT NULL DEFAULT '',
    ledger_transaction_id UUID UNIQUE REFERENCES ledger_transactions(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_payments_user_id ON payments(user_id);
CREATE INDEX IF NOT EXISTS idx_payments_account_id ON payments(account_id);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status);
