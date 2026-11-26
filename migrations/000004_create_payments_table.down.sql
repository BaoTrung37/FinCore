DROP TABLE IF EXISTS payments;

DELETE FROM accounts WHERE account_number IN ('SETTLEMENT-VND', 'SETTLEMENT-USD');
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000001';

DROP INDEX IF EXISTS idx_accounts_type_currency;

ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_balance_amount_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_balance_amount_check CHECK (balance_amount >= 0);
ALTER TABLE accounts DROP COLUMN IF EXISTS account_type;
