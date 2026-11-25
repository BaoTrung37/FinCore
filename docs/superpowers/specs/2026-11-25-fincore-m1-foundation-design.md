# FinCore M1 — Foundation Design

**Date:** 2026-11-25  
**Scope:** Milestone 1 (Foundation) of FinCore — Financial Ledger & Payment Platform (Modular Monolith in Go).

## 1. Intent & Why

Build a clean, testable Go backend foundation for FinCore before implementing financial ledger logic in M2. M1 establishes configuration, structured logging, PostgreSQL connection pooling & embedded migrations, HTTP routing/middleware with graceful shutdown, and JWT-based user authentication (`users`), verified by unit and integration tests.

## 2. Goals, Non-Goals & Anti-Goals

- **Goals:**
  - Load and validate runtime configuration from environment variables (`.env` optional in local dev, env vars authoritative).
  - Structured JSON logging via `log/slog` with request ID correlation.
  - PostgreSQL pool (`pgxpool`) with proper lifecycle management (`Close()` on shutdown) and embedded SQL migrations (`golang-migrate/migrate/v4` + `embed.FS`).
  - HTTP server using `net/http` + `go-chi/chi/v5` with request ID, structured request logging, panic recovery, JSON validation/error responses, `/healthz` (DB ping), and graceful shutdown on `SIGINT`/`SIGTERM`.
  - Auth module (`internal/modules/auth`): user registration (`bcrypt`), login (HS256 JWT), and `GET /api/v1/auth/me` protected by JWT middleware.
  - Unit tests (no DB required) + integration tests against PostgreSQL (auto-skip cleanly when `TEST_DATABASE_URL` / local Postgres is unreachable).
- **Non-Goals (deferred to M2+):**
  - `accounts`, `ledger_transactions`, `ledger_entries`, `payments`, `outbox_events`, `audit_logs`, `sqlc`, Redis, RabbitMQ, Prometheus/OpenTelemetry, refresh tokens/OAuth2.
- **Anti-Goals:**
  - Hardcoded credentials or DSNs in `main.go`.
  - Silent error swallowing or `log.Fatal` inside request/library packages.
  - Leaking `password_hash` or internal SQL errors in HTTP responses.

## 3. Constraints

- Go `1.27.2` (`module github.com/BaoTrung37/FinCore`).
- PostgreSQL 17 (`compose.yaml` already configured).
- Modular Monolith layout: `cmd/api`, `internal/platform/*`, `internal/modules/*`, `migrations/`, `tests/integration/`.
- `migrate`, `sqlc`, `docker` CLIs are not assumed to exist in PATH during build/test.

## 4. Approaches Considered

1. **Hướng A — Embedded Migration (`embed.FS` + `golang-migrate/v4`) + `pgxpool` trực tiếp *(Selected)***
   - *Pros:* Zero external CLI dependency; `go test` and `go run` apply migrations deterministically; easy to run against a clean test DB.
   - *Cons:* `sqlc` is deferred to M2 when complex ledger queries are introduced; production multi-instance deploys should later add a `-migrate-only` flag.
2. **Hướng B — CLI-first (`migrate` CLI + `sqlc` CLI + `Makefile`)**
   - *Pros:* Separates migration & codegen steps from app binary from day one.
   - *Cons:* Requires installing `migrate` and `sqlc` CLIs before running tests; M1 only has 2–3 simple `users` queries.

**Decision:** Hướng A for M1; `sqlc` will be added in M2 without rewriting M1.

## 5. Architecture & Component Decisions

### 5.1 Platform Packages (`internal/platform/`)
- `internal/platform/config`: `Config` struct (`AppEnv`, `HTTPPort`, `DatabaseURL`, `JWTSecret`, `JWTExpiration`, `LogLevel`, `AutoMigrate`). `Load()` reads env vars (loading `.env` if present via `joho/godotenv` or minimal parser — using `joho/godotenv` is standard and tiny), validates required fields (`DATABASE_URL`, `JWT_SECRET` min 32 chars in non-test or explicit validation), and returns typed errors.
- `internal/platform/logger`: `New(level string, env string) *slog.Logger` writing JSON to `os.Stdout` (or text in `local` if desired; JSON default for observability readiness).
- `internal/platform/database`: Rename `progress.go` → `postgres.go`, fix error message (`"ping postgres: %w"`), keep `NewPostgresPool(ctx, connString)`. Add `MigrateUp(ctx, connString, migrationsFS)` using `golang-migrate/migrate/v4` (`iofs` + `pgx/v5` driver).
- `internal/platform/httpserver`:
  - Shared JSON helpers (`WriteJSON`, `WriteError` with consistent `{"error": {"code": ..., "message": ...}}` shape) and request decoding/validation (`go-playground/validator/v10`).
  - Middlewares: `chi/middleware.RequestID`, `middleware.RealIP`, custom `slog` request logger, `middleware.Recoverer`.
  - `GET /healthz`: checks `pool.Ping(ctx)` with short timeout (2s), returns `200 {"status":"ok"}` or `503 {"status":"unavailable"}`.

### 5.2 Database Migration (`migrations/`)
- `000001_create_users_table.up.sql` / `.down.sql`:
  - `users`: `id UUID PRIMARY KEY DEFAULT gen_random_uuid()`, `email TEXT NOT NULL UNIQUE`, `password_hash TEXT NOT NULL`, `status TEXT NOT NULL DEFAULT 'active'`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`.
  - Index on `email` (via `UNIQUE`).

### 5.3 Auth Module (`internal/modules/auth/`)
- `model.go`: `User` struct (never serialize `PasswordHash`), `RegisterRequest`, `LoginRequest`, `AuthResponse`, `UserResponse`.
- `repository.go`: `Repository` interface (`CreateUser`, `GetUserByEmail`, `GetUserByID`) + `PostgresRepository` using `*pgxpool.Pool`, mapping unique violation (`23505`) to `ErrEmailAlreadyExists` and `pgx.ErrNoRows` to `ErrUserNotFound`.
- `jwt.go`: `TokenManager` (HS256 via `golang-jwt/jwt/v5`) with `Generate(userID, email)` and `Verify(tokenString) (*Claims, error)`.
- `service.go`: `Service` handling `Register` (hashes password with `bcrypt.DefaultCost`), `Login` (constant-time bcrypt compare + JWT issue), `GetProfile(ctx, userID)`.
- `middleware.go`: `RequireAuth(TokenManager)` extracting `Authorization: Bearer <token>` and storing `userID` in `context.Context`.
- `handler.go`: `POST /api/v1/auth/register` (`201`), `POST /api/v1/auth/login` (`200`), `GET /api/v1/auth/me` (`200`, protected).

### 5.4 Entrypoint (`cmd/api/main.go`)
- Load `config.Load()`, init `slog`, connect `NewPostgresPool` (`defer pool.Close()`), run `MigrateUp` when `AutoMigrate` is true, wire `auth` module + `chi` router, start `http.Server` with timeouts (`ReadHeaderTimeout: 5s`, `ReadTimeout: 15s`, `WriteTimeout: 15s`, `IdleTimeout: 60s`), handle `SIGINT`/`SIGTERM` with `srv.Shutdown(ctx)` (10s timeout).

## 6. Verification Strategy

- **Unit tests:** `config`, `auth/jwt`, `auth/service` (mock repository), `auth/handler` (`httptest`).
- **Integration tests (`tests/integration/`):** spin up router + real PostgreSQL (`TEST_DATABASE_URL` or default local DSN, `t.Skip` if unreachable), run `MigrateUp`, test `GET /healthz`, `register` → duplicate email (`409`) → `login` (`200`) → `GET /api/v1/auth/me` (`200` with token, `401` without/invalid token).
