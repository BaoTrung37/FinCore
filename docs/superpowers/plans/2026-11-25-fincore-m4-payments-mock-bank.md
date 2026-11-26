# FinCore M4 — Payments & Mock Bank Gateway Implementation Plan

**Goal:** Implement external deposits, withdrawals, settlement accounts, and a deterministic Mock Bank Gateway with idempotency and double-entry ledger integration.

**Architecture:** New `internal/modules/payment` module owning `payments`, coordinating with `account.Service` (customer & settlement account lookup/ownership) and `ledger.Service` (double-entry `deposit`/`withdrawal` posting in a shared `pgx.Tx`).

---

### Task 1: Migration `000004_create_payments_table` & `sqlc.yaml`
- Add `account_type` to `accounts`, relax `balance_amount >= 0` for `settlement` accounts, seed `SETTLEMENT-VND` and `SETTLEMENT-USD`.
- Create `payments` table + indexes.
- Update `sqlc.yaml` with `payment` module entry.

### Task 2: `account` & `ledger` Settlement Support
- Update `internal/modules/account/queries/accounts.sql` and `internal/modules/account/db/` to include `AccountType` and `GetSettlementAccountByCurrency`.
- Add `account.Service.GetSettlementAccountByCurrency(ctx, currency)`.
- Update `internal/modules/ledger/service.go` `postInTx` to allow negative balance projections on `account_type == "settlement"`.

### Task 3: `internal/modules/payment` DB, Gateway, Service & Handler
- `queries/payments.sql` + `db/` models, querier, and SQL implementations.
- `gateway.go`: `BankGateway` interface + `MockBankGateway` (`MOCK_FAIL`, `MOCK_PENDING`, default instant completion) + HMAC-SHA256 callback signature verification.
- `service.go`: `InitiateDeposit`, `InitiateWithdrawal`, `HandleBankCallback`, `GetPayment`, `ListPayments`.
- `handler.go`: `POST /api/v1/payments/deposits`, `POST /api/v1/payments/withdrawals`, `GET /api/v1/payments`, `GET /api/v1/payments/{id}`, `POST /api/v1/payments/callbacks/mock-bank`.

### Task 4: Composition Root, Config, Tests & Docs
- Wire `payment` module in `cmd/api/main.go` and add optional `MOCK_BANK_WEBHOOK_SECRET` in `internal/platform/config/config.go`.
- Add unit tests in `internal/modules/payment/` and integration tests in `tests/integration/payment_test.go`.
- Update `AGENTS.md` and `README.md`.
