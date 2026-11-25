# FinCore M4 — Payments & Mock Bank Gateway Design

**Date:** 2026-11-25  
**Scope:** Phase 4 (External Deposits, Withdrawals & Mock Bank Gateway) on top of M2 Account/Ledger and M3 Transfer Engine.

## 1. Intent & Why

Enable users to move funds between external bank accounts and their FinCore accounts (`deposit` and `withdrawal`) through a pluggable `BankGateway` interface, backed by a deterministic `MockBankGateway` for local development and testing, while preserving strict double-entry ledger invariants and idempotency.

## 2. Goals & Invariants

- **Settlement Accounts (`account_type = 'settlement'`):**
  - `accounts` gains `account_type TEXT NOT NULL DEFAULT 'customer' CHECK (account_type IN ('customer', 'settlement'))`.
  - Balance constraint becomes `CHECK (account_type = 'settlement' OR balance_amount >= 0)` so customer accounts can never go negative, while `settlement` accounts (`SETTLEMENT-VND`, `SETTLEMENT-USD`) track net external liquidity (`SUM(balance_amount) == 0` per currency across all accounts).
- **Endpoints (protected by `auth.RequireAuth` unless noted):**
  - `POST /api/v1/payments/deposits` (requires `Idempotency-Key` header) → `201 Created` on new payment, `200 OK` on idempotent replay.
  - `POST /api/v1/payments/withdrawals` (requires `Idempotency-Key` header) → `201 Created` on new payment, `200 OK` on idempotent replay.
  - `GET /api/v1/payments` → paginated list of authenticated user's payments (`limit`, `offset`).
  - `GET /api/v1/payments/{id}` → get single payment owned by user (`200` / `403` / `404`).
  - `POST /api/v1/payments/callbacks/mock-bank` → settles `pending` payments asynchronously (verified by `X-Mock-Bank-Signature` HMAC-SHA256 when secret is configured, idempotent on already-settled payments).
- **Invariants:**
  - `amount > 0`, `currency` in `('VND', 'USD')`, matches target account currency, account is `active` and owned by the authenticated user.
  - Withdrawals verify `balance_amount >= amount` before initiating bank payout and inside the ledger transaction.
  - Same `(user_id, idempotency_key)` + same `request_hash` (SHA-256 of canonical `direction|account_id|amount|currency|bank_code|bank_account_number`) → returns stored payment without re-calling bank or ledger.
  - Same `(user_id, idempotency_key)` + different `request_hash` → `409 Conflict` (`IDEMPOTENCY_KEY_CONFLICT`).
  - Completed deposits: `debit` `SETTLEMENT-<CURR>`, `credit` `user_account` (`transaction_type = 'deposit'`).
  - Completed withdrawals: `debit` `user_account`, `credit` `SETTLEMENT-<CURR>` (`transaction_type = 'withdrawal'`).

## 3. Database Schema (`migrations/000004_create_payments_table.up.sql`)

- Alter `accounts`: add `account_type`, update balance check constraint, seed system user + `SETTLEMENT-VND` / `SETTLEMENT-USD`.
- Create `payments`:
  - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
  - `user_id UUID NOT NULL REFERENCES users(id)`
  - `account_id UUID NOT NULL REFERENCES accounts(id)`
  - `idempotency_key TEXT NOT NULL`
  - `request_hash TEXT NOT NULL`
  - `direction TEXT NOT NULL CHECK (direction IN ('deposit', 'withdrawal'))`
  - `amount BIGINT NOT NULL CHECK (amount > 0)`
  - `currency CHAR(3) NOT NULL CHECK (currency IN ('VND', 'USD'))`
  - `bank_code TEXT NOT NULL`
  - `bank_account_number TEXT NOT NULL`
  - `external_ref TEXT UNIQUE`
  - `status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'failed'))`
  - `failure_reason TEXT NOT NULL DEFAULT ''`
  - `ledger_transaction_id UUID UNIQUE REFERENCES ledger_transactions(id)`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `UNIQUE (user_id, idempotency_key)`
