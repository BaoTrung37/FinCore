DROP TRIGGER IF EXISTS trg_prevent_ledger_entry_mutation ON ledger_entries;
DROP FUNCTION IF EXISTS prevent_ledger_entry_mutation();
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS ledger_transactions;
DROP TABLE IF EXISTS accounts;
