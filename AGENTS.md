# FinCore — Engineering & Agent Guide

Modular Monolith banking backend in Go (`net/http` + `chi`, PostgreSQL 17, `pgxpool`, `sqlc`, `golang-migrate`, `slog`).

## Architecture & Module Map

- `cmd/api/main.go`: Composition root (config, logger, DB pool, migrations, router, graceful shutdown).
- `internal/platform/`: Shared infrastructure (`config`, `database`, `logger`, `httpserver`). No business logic.
- `internal/modules/`: Domain modules. **Strict boundary:** a module may only write to its own DB tables; cross-module flows coordinate via service/interface calls within a shared `pgx.Tx` when atomicity is required.
  - `auth` → owns `users`
  - `account` (M2) → owns `accounts`
  - `ledger` (M2) → owns `ledger_transactions`, `ledger_entries`
  - `transfer` (M3) → owns `transfers`
  - `payment` (M4) → owns `payments`
  - `webhook` (M5) → owns `webhook_endpoints`, `outbox_events`
  - `reconciliation` (M6) → owns `reconciliation_runs`, `reconciliation_items`, `ledger_audit_runs`
- `migrations/`: Embedded SQL migrations (`embed.FS`, `golang-migrate/migrate/v4`).
- `tests/integration/`: End-to-end tests against PostgreSQL (skip cleanly if DB unreachable).

## Financial Invariants (Non-Negotiable)

1. **Money representation:** Always `int64` in minor units (e.g., VND in đồng, USD in cents) + explicit ISO-4217 `currency`. Never `float64`.
2. **Double-entry balance:** Every `ledger_transaction` must have `>= 2` entries, a single currency, and `SUM(debit) == SUM(credit)`.
3. **Append-only ledger:** Never `UPDATE` or `DELETE` `ledger_entries` (enforced by DB trigger). Corrections use a reversal transaction.
4. **Deadlock-free locking:** When locking multiple accounts with `SELECT ... FOR UPDATE`, always lock in ascending `account_id` order.
5. **Account status:** `frozen` or `closed` accounts cannot debit/credit.

## Standard Commands

```bash
# Generate type-safe SQL code (after editing migrations/ or queries/)
sqlc generate

# Run unit tests
go test -race ./...

# Run integration tests (requires running PostgreSQL)
TEST_DATABASE_URL="postgres://fincore_app:local_dev_password@localhost:5432/fincore?sslmode=disable" \
  go test -race -v ./tests/integration/...
```
