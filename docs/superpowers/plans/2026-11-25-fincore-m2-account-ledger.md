# FinCore M2 — Account & Double-Entry Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `account` module (multi-currency accounts, freeze/unfreeze, ownership isolation, balance endpoint) and the `ledger` module (immutable double-entry bookkeeping, `SUM(debit) == SUM(credit)`, deterministic row locking, balance projection sync, reversal transactions) using `sqlc` on `pgx/v5`.

**Architecture:** Per-module `sqlc` packages (`internal/modules/account/db`, `internal/modules/ledger/db`) generated from `migrations/` and `internal/modules/*/queries/`. Cross-module atomicity between `ledger_entries` and `accounts.balance_amount` uses a shared `pgx.Tx` with `Queries.WithTx(tx)` and deterministic ascending `account_id` lock ordering.

**Tech Stack:** Go `1.27.2`, PostgreSQL 17, `pgx/v5`, `sqlc`, `chi/v5`, `golang-migrate/v4`.

**Spec:** [2026-11-25-fincore-m2-account-ledger-design.md](file:///Users/trungnguyen/Work/GoLang/FinCore/docs/superpowers/specs/2026-11-25-fincore-m2-account-ledger-design.md)

## Global Constraints

- Money is always `int64` in minor units + ISO-4217 `currency` (`VND`, `USD`); never `float64`.
- `ledger_entries` is append-only (enforced by PostgreSQL `BEFORE UPDATE OR DELETE` trigger).
- Every `PostTransaction` must enforce `len(entries) >= 2`, single currency, `amount > 0`, `SUM(debit) == SUM(credit)`.
- Account locking must sort `account_id`s ascending before `SELECT ... FOR UPDATE`.
- Every `account` HTTP endpoint requires `auth.RequireAuth` and enforces `account.UserID == authUserID`.

## Review Focus

1. Unbalanced ledger transaction (`SUM(debit) != SUM(credit)`) or `< 2` entries or `amount <= 0` → rejected before DB write with domain error.
2. Mixed-currency entries in a single transaction or entry currency != account currency → rejected before balance mutation.
3. Frozen/closed account targeted by `debit` or `credit` → rejected (`ErrAccountFrozen` / `ErrAccountClosed`); insufficient balance on `debit` → rejected (`ErrInsufficientBalance`).
4. Cross-user account access (`GET/POST /api/v1/accounts/{id}*` by non-owner) → `403 Forbidden` (or `404` for invalid UUID).
5. Concurrent reversal of the same `ledger_transaction` → `reversal_of_id UNIQUE` prevents double-reversal; DB trigger blocks any `UPDATE`/`DELETE` on `ledger_entries`.

---

### Task 1: Schema Migration (`000002`) & `sqlc` Code Generation

**Files:**
- Create: `migrations/000002_create_accounts_and_ledger.up.sql`
- Create: `migrations/000002_create_accounts_and_ledger.down.sql`
- Create: `internal/modules/account/queries/accounts.sql`
- Create: `internal/modules/ledger/queries/ledger.sql`
- Generate: `internal/modules/account/db/*` and `internal/modules/ledger/db/*` via `sqlc generate`

**Interfaces:**
- Produces: `accountdb.Querier`, `accountdb.New`, `ledgerdb.Querier`, `ledgerdb.New`

- [ ] **Step 1: Write `000002_create_accounts_and_ledger.{up,down}.sql`**
  - Define `accounts`, `ledger_transactions`, `ledger_entries` with all `CHECK` constraints, `UNIQUE` constraints (`account_number`, `reference`, `reversal_of_id`), indexes (`accounts(user_id)`, `ledger_entries(transaction_id)`, `ledger_entries(account_id)`), and the `prevent_ledger_entry_mutation()` trigger on `ledger_entries`.
- [ ] **Step 2: Write `sqlc` query files**
  - `internal/modules/account/queries/accounts.sql`: `CreateAccount`, `ListAccountsByUserID`, `GetAccountByID`, `GetAccountsForUpdate` (`WHERE id = ANY($1::uuid[]) ORDER BY id ASC FOR UPDATE`), `UpdateAccountStatus`, `UpdateAccountBalance` (`SET balance_amount = balance_amount + $2, updated_at = now() WHERE id = $1`).
  - `internal/modules/ledger/queries/ledger.sql`: `CreateLedgerTransaction`, `GetLedgerTransactionByID`, `MarkLedgerTransactionReversed`, `CreateLedgerEntry`, `ListEntriesByTransactionID`, `ListEntriesByAccountID`.
- [ ] **Step 3: Run `sqlc generate` and verify compilation**
  - Run: `sqlc generate && go build ./...`
  - Expected: Exit 0, generated files in `internal/modules/account/db` and `internal/modules/ledger/db`.
- [ ] **Step 4: Commit**
  - `git add migrations internal/modules/account/queries internal/modules/account/db internal/modules/ledger/queries internal/modules/ledger/db sqlc.yaml`
  - `git commit -m "feat(m2): add accounts & ledger migration and sqlc queries"`

---

### Task 2: Account Module (Domain, Service, HTTP Handlers & Tests)

**Files:**
- Create: `internal/modules/account/service.go`
- Create: `internal/modules/account/handler.go`
- Test: `internal/modules/account/service_test.go`
- Test: `internal/modules/account/handler_test.go`

**Interfaces:**
- Consumes: `accountdb.Querier`, `auth.RequireAuth`, `auth.UserIDFromContext`, `httpserver.*`
- Produces: `account.NewService(q accountdb.Querier)`, `account.NewHandler(svc *Service, log *slog.Logger)`, `(*Handler).RegisterRoutes(r chi.Router, tm *auth.TokenManager)`

- [ ] **Step 1: Write failing service & handler tests (RED)**
  - `service_test.go`: create account (valid VND/USD, invalid currency), freeze/unfreeze (reject non-owner with `ErrForbidden`), get balance (owner vs non-owner).
  - `handler_test.go`: `POST /api/v1/accounts` (`201`, `400` invalid currency, `401` unauth), `GET /api/v1/accounts` (`200`), `GET /api/v1/accounts/{id}` (`403` non-owner, `400`/`404` bad UUID), `POST /freeze` & `/unfreeze` (`200`), `GET /balance` (`200`).
- [ ] **Step 2: Run tests to verify RED**
  - Run: `go test ./internal/modules/account/...` → FAIL.
- [ ] **Step 3: Implement `service.go` and `handler.go` (GREEN)**
  - Generate unique `account_number` (e.g., `FC` + 12 random digits via `crypto/rand`), enforce ownership on all single-account operations, validate `currency` in `{"VND", "USD"}`.
- [ ] **Step 4: Run tests to verify GREEN & commit**
  - Run: `go test -race -v ./internal/modules/account/...` → PASS.
  - `git add internal/modules/account && git commit -m "feat(account): implement account management service and endpoints"`

---

### Task 3: Double-Entry Ledger Service & Unit Tests

**Files:**
- Create: `internal/modules/ledger/service.go`
- Test: `internal/modules/ledger/service_test.go`

**Interfaces:**
- Consumes: `pgxpool.Pool`, `accountdb.Querier`, `ledgerdb.Querier`
- Produces:
  - `ledger.EntryInput` (`AccountID string`, `EntryType string`, `Amount int64`, `Currency string`)
  - `ledger.PostTransactionInput` (`Reference string`, `TransactionType string`, `Entries []EntryInput`)
  - `ledger.NewService(pool *pgxpool.Pool)`
  - `(*Service).PostTransaction(ctx context.Context, in PostTransactionInput) (ledgerdb.LedgerTransaction, []ledgerdb.LedgerEntry, error)`
  - `(*Service).ReverseTransaction(ctx context.Context, txID string, reference string) (ledgerdb.LedgerTransaction, []ledgerdb.LedgerEntry, error)`

- [ ] **Step 1: Write failing unit tests for ledger invariants (RED)**
  - `service_test.go` (testing validation + mocked tx runner):
    - Reject `< 2` entries (`ErrInvalidEntries`)
    - Reject `amount <= 0` (`ErrInvalidAmount`)
    - Reject mixed currencies (`ErrCurrencyMismatch`)
    - Reject `SUM(debit) != SUM(credit)` (`ErrUnbalancedTransaction`)
    - Verify deterministic ascending sort of `account_id`s before locking.
- [ ] **Step 2: Run tests to verify RED**
  - Run: `go test ./internal/modules/ledger/...` → FAIL.
- [ ] **Step 3: Implement `internal/modules/ledger/service.go` (GREEN)**
  - Validate inputs before opening `pgx.Tx`; inside `pgx.Tx`, lock accounts in ascending `account_id` order (`GetAccountsForUpdate`), verify all accounts exist, `status == "active"`, `account.Currency == txCurrency`, and `balance_amount >= totalDebitForAccount`; insert `ledger_transaction` + `ledger_entries`; update `accounts.balance_amount`; commit.
  - `ReverseTransaction`: load original tx + entries inside `pgx.Tx`, fail if already reversed (`ErrAlreadyReversed`), flip `debit` ↔ `credit`, and post reversal tx with `reversal_of_id`.
- [ ] **Step 4: Run tests to verify GREEN & commit**
  - Run: `go test -race -v ./internal/modules/ledger/...` → PASS.
  - `git add internal/modules/ledger && git commit -m "feat(ledger): implement double-entry ledger posting and reversal"`

---

### Task 4: Wire Account Routes in `cmd/api/main.go` & PostgreSQL Integration Tests

**Files:**
- Modify: `cmd/api/main.go`
- Create: `tests/integration/account_ledger_integration_test.go`

**Interfaces:**
- Consumes: `account.NewService`, `account.NewHandler`, `ledger.NewService`

- [ ] **Step 1: Write `tests/integration/account_ledger_integration_test.go`**
  - Tests against real PostgreSQL (skip cleanly if unreachable):
    1. Account lifecycle: create 2 VND accounts -> freeze -> verify `ledger.PostTransaction` rejects frozen account -> unfreeze.
    2. Double-entry posting: deposit (`system` or credit/debit pair) / transfer pair -> verify `SUM(debit) == SUM(credit)` and `accounts.balance_amount` matches `SUM(credit) - SUM(debit)`.
    3. Append-only enforcement: direct `UPDATE ledger_entries` and `DELETE FROM ledger_entries` fail with DB trigger error.
    4. Reversal: `ReverseTransaction` restores balances and rejects second reversal (`ErrAlreadyReversed`).
    5. Ownership: user B gets `403` on user A's account endpoints.
- [ ] **Step 2: Wire `account` routes in `cmd/api/main.go`**
  - Initialize `accountdb.New(pool)`, `account.NewService`, `account.NewHandler`, and call `accountHandler.RegisterRoutes(router, tm)`.
- [ ] **Step 3: Build & run full test suite**
  - Run: `go build ./... && go test -race -v ./...`
  - Expected: `go build ./...` exits 0; all unit tests PASS; integration tests PASS (or skip cleanly if DB unreachable).
- [ ] **Step 4: Commit**
  - `git add cmd/api/main.go tests/integration/account_ledger_integration_test.go`
  - `git commit -m "feat(m2): wire account routes and add account/ledger integration tests"`
