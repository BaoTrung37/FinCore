# FinCore — Financial Ledger & Payment Platform

Modular Monolith banking backend in Go (`net/http` + `chi`, PostgreSQL 17, `pgxpool`, `golang-migrate`, `slog`). Designed with strict financial correctness (double-entry ledger, integer minor units, idempotency, transactional outbox) and extensible into **FinMatch** (bank transaction reconciliation SaaS).

## Prerequisites

- Go `1.27+`
- Docker & Docker Compose (for local PostgreSQL 17)

## Quick Start

1. **Configure environment:**
   ```bash
   cp .env.example .env
   ```

2. **Start PostgreSQL:**
   ```bash
   docker compose up -d
   ```

3. **Run API server** (embedded SQL migrations run automatically when `AUTO_MIGRATE=true`):
   ```bash
   go run ./cmd/api
   ```

4. **Verify health:**
   ```bash
   curl -i http://localhost:8080/healthz
   ```

## Running Tests

```bash
# Unit tests (no database required)
go test -race ./...

# Integration tests (requires running PostgreSQL; skips cleanly if unreachable)
TEST_DATABASE_URL="postgres://fincore_app:local_dev_password@localhost:5432/fincore?sslmode=disable" \
  go test -race -v ./tests/integration/...
```

## Implemented Milestones

- **M1 — Foundation (`feat/m1-foundation`):**
  - Env config validation (`DATABASE_URL`, `JWT_SECRET`, `JWT_EXPIRATION`)
  - Structured JSON logging (`log/slog`) with `X-Request-Id`
  - `pgxpool` connection pool + embedded SQL migrations (`users`)
  - `chi` HTTP server, `http.MaxBytesReader`, `/healthz`, graceful shutdown
  - Auth module (`POST /api/v1/auth/register`, `POST /api/v1/auth/login`, `GET /api/v1/auth/me`)
