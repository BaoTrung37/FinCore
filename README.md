# FinCore — Financial Ledger & Payment Platform

Modular Monolith banking backend in Go (`net/http` + `chi`, PostgreSQL 17, `pgxpool`, `golang-migrate`, `slog`). Designed with strict financial correctness (double-entry ledger, integer minor units, idempotency, transactional outbox) and extensible into **FinMatch** (bank transaction reconciliation SaaS).

## Prerequisites

- Go `1.27+`
- Docker & Docker Compose (for local PostgreSQL 17)

## Quick Start

```bash
# First-time setup on a new machine (.env, Go modules, sqlc, PostgreSQL)
make setup

# Start PostgreSQL (if not running) and run the API server
make dev
```

Verify health:
```bash
curl -i http://localhost:8080/healthz
```

## Running Tests

```bash
# Unit tests (no database required)
make test

# Integration tests (starts PostgreSQL via Docker Compose)
make test-integration
```

## Implemented Milestones

- **M1 — Foundation (`feat/m1-foundation`):**
  - Env config validation (`DATABASE_URL`, `JWT_SECRET`, `JWT_EXPIRATION`)
  - Structured JSON logging (`log/slog`) with `X-Request-Id`
  - `pgxpool` connection pool + embedded SQL migrations (`users`)
  - `chi` HTTP server, `http.MaxBytesReader`, `/healthz`, graceful shutdown
  - Auth module (`POST /api/v1/auth/register`, `POST /api/v1/auth/login`, `GET /api/v1/auth/me`)
- **M2 — Account & Double-Entry Ledger (`feat/m2-account-ledger`):**
  - Account lifecycle (`POST /api/v1/accounts`, `GET /api/v1/accounts`, `GET /api/v1/accounts/{id}`, `freeze`, `unfreeze`, `balance`, `transactions`)
  - Append-only double-entry ledger (`ledger_transactions`, `ledger_entries`) with `SUM(debit) == SUM(credit)` and reversal support
- **M3 — Transfer Engine (`feat/m3-transfer-engine`):**
  - Atomic idempotent internal transfers (`POST /api/v1/transfers`, `GET /api/v1/transfers/{id}`) with ascending `account_id` row locking
- **M4 — Payments & Mock Bank Gateway (`feat/m4-payments`):**
  - Deposits & withdrawals (`POST /api/v1/payments/deposits`, `POST /api/v1/payments/withdrawals`, `GET /api/v1/payments`, `GET /api/v1/payments/{id}`)
  - Signed mock bank callback webhook (`POST /api/v1/payments/callbacks/mock-bank`)
- **M5 — Transactional Outbox, Webhooks & API Docs (`feat/m5-outbox-webhooks`):**
  - Webhook subscription management (`POST /api/v1/webhooks`, `GET /api/v1/webhooks`, `DELETE /api/v1/webhooks/{id}`)
  - Transactional outbox (`outbox_events`) published atomically from transfers & payments, with `RelayWorker` (`FOR UPDATE SKIP LOCKED`, HMAC-SHA256 signing, exponential backoff)
  - Embedded OpenAPI spec & Swagger UI (`/openapi.json`, `/swagger`)
- **M6 — Reconciliation Engine (`feat/m6-backend`):**
  - External bank statement reconciliation via JSON & CSV (`POST /api/v1/reconciliation/runs`, `POST /api/v1/reconciliation/runs/csv`, `GET /api/v1/reconciliation/runs`, `GET /api/v1/reconciliation/runs/{id}`)
  - Discrepancy resolution workflow (`PATCH /api/v1/reconciliation/runs/{id}/items/{item_id}`)
  - Internal double-entry & balance-drift ledger integrity audit (`POST /api/v1/reconciliation/ledger-audit`, `GET /api/v1/reconciliation/ledger-audit`) with transactional outbox notifications

## Documentation

- [Database ERD](docs/erd.md) — Entity-Relationship Diagram, module ownership, constraints, triggers, and indexes across all migrations.
