# FinCore M2 — Account & Double-Entry Ledger Design

**Date:** 2026-11-25  
**Scope:** Phase 2 (Account Management + Double-Entry Ledger) on top of M1 Foundation.

## 1. Intent & Why

Implement the financial core of FinCore: user-owned multi-currency accounts (with freeze/unfreeze lifecycle and ownership isolation) and an immutable, balanced double-entry ledger (`ledger_transactions` + `ledger_entries`) with reversal support.

## 2. Goals, Non-Goals & Anti-Goals

- **Goals:**
  - `account` module: create account (VND/USD), list own accounts, get account detail, freeze/unfreeze, get balance, enforce ownership (`user_id == authUserID`) and status checks.
  - `ledger` module: post balanced double-entry transactions (`SUM(debit) == SUM(credit)`, `>= 2` entries, single currency, `amount > 0`), append-only DB trigger on `ledger_entries`, deterministic `SELECT ... FOR UPDATE` ordering by `account_id`, balance projection update inside the same `pgx.Tx`, and `ReverseTransaction`.
  - Use `sqlc` (`pgx/v5`) for all account and ledger SQL queries.
- **Non-Goals (deferred to M3+):**
  - Public `POST /api/v1/transfers` & idempotency keys (M3), payments/mock bank (M4), transactional outbox/webhooks (M5), reconciliation (M6).
- **Anti-Goals:**
  - Floating-point money (`float64`).
  - Updating `accounts.balance_amount` without matching `ledger_entries`.
  - Allowing `UPDATE`/`DELETE` on `ledger_entries`.
  - Mixing currencies within a single ledger transaction.

## 3. Database Schema (`migrations/000002_create_accounts_and_ledger.up.sql`)

- `accounts`:
  - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
  - `user_id UUID NOT NULL REFERENCES users(id)`
  - `account_number TEXT NOT NULL UNIQUE`
  - `currency CHAR(3) NOT NULL CHECK (currency IN ('VND', 'USD'))`
  - `status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen', 'closed'))`
  - `balance_amount BIGINT NOT NULL DEFAULT 0 CHECK (balance_amount >= 0)`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `ledger_transactions`:
  - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
  - `reference TEXT NOT NULL UNIQUE`
  - `transaction_type TEXT NOT NULL CHECK (transaction_type IN ('deposit', 'withdrawal', 'transfer', 'reversal', 'adjustment'))`
  - `status TEXT NOT NULL DEFAULT 'posted' CHECK (status IN ('posted', 'reversed'))`
  - `reversal_of_id UUID UNIQUE REFERENCES ledger_transactions(id)` (prevents double-reversal at DB level)
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `ledger_entries`:
  - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
  - `transaction_id UUID NOT NULL REFERENCES ledger_transactions(id)`
  - `account_id UUID NOT NULL REFERENCES accounts(id)`
  - `entry_type TEXT NOT NULL CHECK (entry_type IN ('debit', 'credit'))`
  - `amount BIGINT NOT NULL CHECK (amount > 0)`
  - `currency CHAR(3) NOT NULL CHECK (currency IN ('VND', 'USD'))`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- **Append-only trigger:** `BEFORE UPDATE OR DELETE ON ledger_entries` raises exception (`ledger_entries are immutable`).

## 4. API Surface (Protected by `auth.RequireAuth`)

- `POST /api/v1/accounts` → create account (`currency`: `VND` | `USD`) → `201`
- `GET /api/v1/accounts` → list authenticated user's accounts → `200`
- `GET /api/v1/accounts/{id}` → get account (ownership enforced) → `200` / `403` / `404`
- `POST /api/v1/accounts/{id}/freeze` → freeze account → `200`
- `POST /api/v1/accounts/{id}/unfreeze` → unfreeze account → `200`
- `GET /api/v1/accounts/{id}/balance` → `{account_id, currency, balance_amount, status}` → `200`

*(Ledger posting & reversal are exposed as domain services in `internal/modules/ledger` and tested via unit + PostgreSQL integration tests; public transfer/payment endpoints are wired in M3/M4.)*

## 5. Key Design Decisions

1. **Per-module `sqlc` packages** (`internal/modules/account/db`, `internal/modules/ledger/db`) enforce table ownership boundaries while sharing `pgx.Tx` via `Queries.WithTx(tx)`.
2. **Debit/Credit sign convention (asset/wallet liability perspective):** `credit` increases `accounts.balance_amount` (`+amount`); `debit` decreases `accounts.balance_amount` (`-amount`), and `balance_amount >= 0` is enforced both in Go and by DB `CHECK`.
3. **Deadlock prevention:** `ledger.Service.PostTransaction` deduplicates and sorts target `account_id`s lexicographically before issuing `SELECT ... FOR UPDATE` inside `pgx.Tx`.
4. **Reversal safety:** `reversal_of_id UUID UNIQUE` guarantees a transaction can be reversed at most once even under concurrent calls, and flips `debit` ↔ `credit` for every original entry.
