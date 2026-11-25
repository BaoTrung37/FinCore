# FinCore M3 — Transfer Engine, Concurrency & Idempotency Design

**Date:** 2026-11-25  
**Scope:** Phase 3 (Internal Transfer + Concurrency Control + Idempotency) on top of M2 Account & Ledger.

## 1. Intent & Why

Implement atomic internal money transfers between accounts with strict idempotency (retry-safe API under duplicate/concurrent requests) and deadlock-free concurrency control (`SELECT ... FOR UPDATE` ordered by `account_id`), backed by M2's double-entry `ledger.Service`.

## 2. Goals & Invariants

- **Endpoints (protected by `auth.RequireAuth`):**
  - `POST /api/v1/transfers` (requires `Idempotency-Key` header) → `201 Created` on new transfer, `200 OK` on idempotent replay.
  - `GET /api/v1/transfers/{id}` → get transfer owned by user (source or destination account owner) → `200` / `403` / `404`.
  - `GET /api/v1/accounts/{id}/transactions` → paginated ledger entries for an owned account (`limit`, `offset`) → `200` / `403` / `404`.
- **Invariants:**
  - `source_account_id != destination_account_id` (`400 SELF_TRANSFER_NOT_ALLOWED`).
  - Authenticated user must own `source_account_id` (`403 FORBIDDEN`).
  - `amount > 0`, `currency` matches both accounts, both accounts `active`, `balance_amount >= amount`.
  - Same `(user_id, idempotency_key)` + same `request_hash` (SHA-256 of canonical `source|dest|amount|currency`) → returns stored transfer without re-executing ledger write.
  - Same `(user_id, idempotency_key)` + different `request_hash` → `409 Conflict` (`IDEMPOTENCY_KEY_CONFLICT`).
  - Concurrent `100` transfers from one account with limited balance: zero negative balances, exact sum conservation (`A + B == initial`).

## 3. Database Schema (`migrations/000003_create_transfers_table.up.sql`)

- `transfers`:
  - `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`
  - `user_id UUID NOT NULL REFERENCES users(id)`
  - `idempotency_key TEXT NOT NULL`
  - `request_hash TEXT NOT NULL`
  - `source_account_id UUID NOT NULL REFERENCES accounts(id)`
  - `destination_account_id UUID NOT NULL REFERENCES accounts(id)`
  - `amount BIGINT NOT NULL CHECK (amount > 0)`
  - `currency CHAR(3) NOT NULL CHECK (currency IN ('VND', 'USD'))`
  - `status TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('completed', 'failed'))`
  - `ledger_transaction_id UUID NOT NULL UNIQUE REFERENCES ledger_transactions(id)`
  - `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
  - `UNIQUE (user_id, idempotency_key)`
