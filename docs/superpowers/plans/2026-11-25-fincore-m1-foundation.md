# FinCore M1 — Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and verify the M1 Foundation for FinCore: env config, structured `slog` logging, `pgxpool` + embedded `golang-migrate` SQL migrations, `chi` HTTP server with `/healthz` and graceful shutdown, and JWT-based `auth` (`register`, `login`, `me`) backed by unit and integration tests.

**Architecture:** Modular Monolith in Go (`cmd/api` entrypoint, `internal/platform/*` shared infrastructure, `internal/modules/auth` domain module, `migrations/` embedded SQL, `tests/integration/` end-to-end tests). Migrations use `embed.FS` + `golang-migrate/migrate/v4` (`iofs` + `pgx/v5`) so builds and tests require no external CLI binaries.

**Tech Stack:** Go `1.27.2`, `github.com/go-chi/chi/v5`, `github.com/jackc/pgx/v5/pgxpool`, `github.com/golang-migrate/migrate/v4`, `github.com/golang-jwt/jwt/v5`, `github.com/go-playground/validator/v10`, `github.com/joho/godotenv`, `golang.org/x/crypto/bcrypt`, PostgreSQL 17.

**Spec:** [2026-11-25-fincore-m1-foundation-design.md](file:///Users/trungnguyen/Work/GoLang/FinCore/docs/superpowers/specs/2026-11-25-fincore-m1-foundation-design.md)

## Global Constraints

- Go `1.27.2`, module `github.com/BaoTrung37/FinCore`.
- No hardcoded DSNs or secrets in `cmd/api/main.go`.
- Never serialize `password_hash` or expose raw SQL errors in HTTP responses.
- No dependency on `migrate`, `sqlc`, or `docker` CLI binaries in PATH.
- Integration tests must skip cleanly (`t.Skip`) when PostgreSQL is unreachable.

## Review Focus

1. Missing `DATABASE_URL` or weak `JWT_SECRET` (< 32 bytes) → `config.Load()` fails fast with descriptive error.
2. Duplicate email registration (Postgres SQLSTATE `23505`) → `409 Conflict`, never `500`.
3. Missing/malformed/expired/wrong-alg JWT → `401 Unauthorized` (explicitly enforce `jwt.SigningMethodHS256`).
4. Malformed JSON or invalid payload (invalid email, password < 8 chars) → `400 Bad Request` with structured JSON error.
5. `GET /healthz` when DB is down/slow → 2s context timeout, returns `503 Service Unavailable`.

---

### Task 1: Configuration & Structured Logger

**Files:**
- Create: `internal/platform/config/config.go`
- Test: `internal/platform/config/config_test.go`
- Create: `internal/platform/logger/logger.go`
- Test: `internal/platform/logger/logger_test.go`
- Modify: `.env.example`

**Interfaces:**
- Produces:
  - `config.Config` struct (`AppEnv`, `HTTPPort`, `DatabaseURL`, `JWTSecret`, `JWTExpiration time.Duration`, `LogLevel`, `AutoMigrate bool`)
  - `config.Load() (*Config, error)`
  - `logger.New(level string, env string) *slog.Logger`

- [ ] **Step 1: Install `godotenv` and write failing config & logger tests**

Run: `go get github.com/joho/godotenv`

Create `internal/platform/config/config_test.go`:
```go
package config_test

import (
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/platform/config"
)

func TestLoad_Success(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/fincore?sslmode=disable")
	t.Setenv("JWT_SECRET", "12345678901234567890123456789012")
	t.Setenv("JWT_EXPIRATION", "30m")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("AUTO_MIGRATE", "false")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.HTTPPort != "9090" || cfg.JWTExpiration != 30*time.Minute || cfg.AutoMigrate {
		t.Fatalf("unexpected config values: %+v", cfg)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "12345678901234567890123456789012")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}

func TestLoad_WeakJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/fincore")
	t.Setenv("JWT_SECRET", "too-short")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected error for weak JWT_SECRET (< 32 chars)")
	}
}
```

Create `internal/platform/logger/logger_test.go`:
```go
package logger_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/platform/logger"
)

func TestNew_RespectsLevel(t *testing.T) {
	log := logger.New("warn", "production")
	if log.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected info level to be disabled when level=warn")
	}
	if !log.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("expected warn level to be enabled")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/platform/config/... ./internal/platform/logger/...`
Expected: FAIL (packages/functions not defined).

- [ ] **Step 3: Implement `config.Load()`, `logger.New()`, and update `.env.example`**

- `internal/platform/config/config.go`: Call `godotenv.Load()` (ignore `os.ErrNotExist`), read env vars with defaults (`APP_ENV=local`, `HTTP_PORT=8080`, `JWT_EXPIRATION=24h`, `LOG_LEVEL=info`, `AUTO_MIGRATE=true`), validate `DatabaseURL != ""` and `len(JWTSecret) >= 32`.
- `internal/platform/logger/logger.go`: Parse `level` (`debug`, `info`, `warn`, `error`, default `info`), return `slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))`.
- `.env.example`: Add `APP_ENV`, `HTTP_PORT`, `DATABASE_URL`, `JWT_SECRET`, `JWT_EXPIRATION`, `LOG_LEVEL`, `AUTO_MIGRATE` alongside existing `POSTGRES_*` vars.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/platform/config/... ./internal/platform/logger/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/config internal/platform/logger .env.example go.mod go.sum
git commit -m "feat(platform): add env config loader and structured slog logger"
```

---

### Task 2: Database Pool & Embedded Migrations

**Files:**
- Rename/Modify: `internal/platform/database/progress.go` → `internal/platform/database/postgres.go`
- Create: `internal/platform/database/migrate.go`
- Test: `internal/platform/database/migrate_test.go`
- Create: `migrations/embed.go`
- Create: `migrations/000001_create_users_table.up.sql`
- Create: `migrations/000001_create_users_table.down.sql`

**Interfaces:**
- Produces:
  - `database.NewPostgresPool(ctx context.Context, connString string) (*pgxpool.Pool, error)`
  - `database.MigrateUp(connString string, migrationsFS fs.FS) error`
  - `migrations.FS` (`embed.FS`)

- [ ] **Step 1: Install `golang-migrate` and write failing migration test**

Run: `go get github.com/golang-migrate/migrate/v4 github.com/golang-migrate/migrate/v4/database/pgx/v5`

Create `internal/platform/database/migrate_test.go`:
```go
package database_test

import (
	"io/fs"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestMigrationsEmbed_ContainsUsersMigration(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least up and down migration files, got %d", len(entries))
	}
}

func TestMigrateUp_InvalidDSN(t *testing.T) {
	err := database.MigrateUp("not-a-valid-dsn", migrations.FS)
	if err == nil {
		t.Fatal("expected error for invalid DSN")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/platform/database/...`
Expected: FAIL (`migrations` package and `MigrateUp` not defined).

- [ ] **Step 3: Create `migrations/`, rename `progress.go` → `postgres.go`, and implement `MigrateUp`**

- `migrations/embed.go`: `package migrations` with `//go:embed *.sql` and `var FS embed.FS`.
- `migrations/000001_create_users_table.up.sql`:
  ```sql
  CREATE EXTENSION IF NOT EXISTS pgcrypto;

  CREATE TABLE IF NOT EXISTS users (
      id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      email TEXT NOT NULL UNIQUE,
      password_hash TEXT NOT NULL,
      status TEXT NOT NULL DEFAULT 'active',
      created_at TIMESTAMPTZ NOT NULL DEFAULT now()
  );
  ```
- `migrations/000001_create_users_table.down.sql`: `DROP TABLE IF EXISTS users;`
- `internal/platform/database/postgres.go`: Move `progress.go` here and fix `"ping postgres: %w"`.
- `internal/platform/database/migrate.go`: Use `iofs.New(migrationsFS, ".")` and `migrate.NewWithSourceInstance("iofs", src, pgx5URL)` (converting `postgres://` to `pgx5://` or `pgx5.Postgres` driver), call `m.Up()`, ignore `migrate.ErrNoChange`, and close `m.Close()`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/platform/database/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/database migrations go.mod go.sum
git commit -m "feat(database): add embedded migrations and fix postgres pool naming"
```

---

### Task 3: HTTP Server, JSON Helpers, Middleware & `/healthz`

**Files:**
- Create: `internal/platform/httpserver/response.go`
- Create: `internal/platform/httpserver/middleware.go`
- Create: `internal/platform/httpserver/router.go`
- Test: `internal/platform/httpserver/httpserver_test.go`

**Interfaces:**
- Produces:
  - `httpserver.ErrorResponse` (`Error struct { Code string; Message string }`)
  - `httpserver.WriteJSON(w http.ResponseWriter, status int, data any)`
  - `httpserver.WriteError(w http.ResponseWriter, status int, code, message string)`
  - `httpserver.DecodeAndValidate(r *http.Request, dst any) error`
  - `httpserver.HealthChecker` interface (`Ping(ctx context.Context) error`)
  - `httpserver.NewRouter(log *slog.Logger, db HealthChecker) *chi.Mux`

- [ ] **Step 1: Install `chi/v5` and `validator/v10`, write failing HTTP server tests**

Run: `go get github.com/go-chi/chi/v5 github.com/go-playground/validator/v10`

Create `internal/platform/httpserver/httpserver_test.go`:
```go
package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
)

type stubHealth struct{ err error }

func (s stubHealth) Ping(_ context.Context) error { return s.err }

func TestHealthz_OKAndUnavailable(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	rOK := httpserver.NewRouter(log, stubHealth{err: nil})
	rec := httptest.NewRecorder()
	rOK.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Fatal("expected X-Request-Id header")
	}

	rFail := httpserver.NewRouter(log, stubHealth{err: errors.New("db down")})
	recFail := httptest.NewRecorder()
	rFail.ServeHTTP(recFail, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recFail.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", recFail.Code)
	}
}

func TestDecodeAndValidate_RejectsInvalidPayload(t *testing.T) {
	type reqBody struct {
		Email string `json:"email" validate:"required,email"`
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"bad"}`))
	var dst reqBody
	if err := httpserver.DecodeAndValidate(req, &dst); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestWriteError_Format(t *testing.T) {
	rec := httptest.NewRecorder()
	httpserver.WriteError(rec, http.StatusBadRequest, "BAD_INPUT", "invalid field")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var out httpserver.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if out.Error.Code != "BAD_INPUT" || out.Error.Message != "invalid field" {
		t.Fatalf("unexpected error body: %+v", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/platform/httpserver/...`
Expected: FAIL (`httpserver` package not defined).

- [ ] **Step 3: Implement `response.go`, `middleware.go`, `router.go`**

- `response.go`: `WriteJSON`, `WriteError`, `DecodeAndValidate` (limit body with `http.MaxBytesReader` 1MB, `DisallowUnknownFields()`, `validator.New().Struct(dst)`).
- `middleware.go`: `RequestLogger(log *slog.Logger)` wrapping `chi/middleware.NewWrapResponseWriter` to log `method`, `path`, `status`, `duration_ms`, `request_id`.
- `router.go`: `NewRouter` attaching `middleware.RequestID`, `middleware.RealIP`, `RequestLogger(log)`, `middleware.Recoverer`, setting `X-Request-Id` on responses, and registering `GET /healthz` with a 2s `context.WithTimeout` ping.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/platform/httpserver/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/httpserver go.mod go.sum
git commit -m "feat(httpserver): add chi router, middleware, JSON helpers, and /healthz"
```

---

### Task 4: Auth JWT TokenManager & Auth Middleware

**Files:**
- Create: `internal/modules/auth/jwt.go`
- Create: `internal/modules/auth/middleware.go`
- Test: `internal/modules/auth/jwt_test.go`

**Interfaces:**
- Produces:
  - `auth.Claims` (`UserID string`, `Email string`, `jwt.RegisteredClaims`)
  - `auth.NewTokenManager(secret string, ttl time.Duration) *TokenManager`
  - `(*TokenManager).Generate(userID, email string) (string, error)`
  - `(*TokenManager).Verify(tokenString string) (*Claims, error)`
  - `auth.RequireAuth(tm *TokenManager) func(http.Handler) http.Handler`
  - `auth.UserIDFromContext(ctx context.Context) (string, bool)`

- [ ] **Step 1: Install `golang-jwt/jwt/v5` and write failing JWT & middleware tests**

Run: `go get github.com/golang-jwt/jwt/v5`

Create `internal/modules/auth/jwt_test.go`:
```go
package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
)

func TestTokenManager_GenerateAndVerify(t *testing.T) {
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	tok, err := tm.Generate("user-123", "a@example.com")
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	claims, err := tm.Verify(tok)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if claims.UserID != "user-123" || claims.Email != "a@example.com" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestTokenManager_RejectsExpiredAndTampered(t *testing.T) {
	tmExpired := auth.NewTokenManager("12345678901234567890123456789012", -time.Minute)
	tok, err := tmExpired.Generate("user-123", "a@example.com")
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if _, err := tmExpired.Verify(tok); err == nil {
		t.Fatal("expected expired token to fail verification")
	}

	tmOther := auth.NewTokenManager("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Hour)
	if _, err := tmOther.Verify(tok); err == nil {
		t.Fatal("expected wrong-secret token to fail verification")
	}
}

func TestRequireAuth_Middleware(t *testing.T) {
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	tok, _ := tm.Generate("user-123", "a@example.com")

	protected := auth.RequireAuth(tm)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := auth.UserIDFromContext(r.Context())
		if !ok || uid != "user-123" {
			t.Fatalf("expected user-123 in context, got %q (%v)", uid, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))

	recMissing := httptest.NewRecorder()
	protected.ServeHTTP(recMissing, httptest.NewRequest(http.MethodGet, "/me", nil))
	if recMissing.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", recMissing.Code)
	}

	recOK := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	protected.ServeHTTP(recOK, req)
	if recOK.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", recOK.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/modules/auth/...`
Expected: FAIL (`auth` package not defined).

- [ ] **Step 3: Implement `jwt.go` and `middleware.go`**

- `jwt.go`: Sign with `jwt.SigningMethodHS256`; in `Verify`, pass `jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()})`.
- `middleware.go`: Parse `Bearer <token>` from `Authorization` header, verify with `tm.Verify`, return `401` via `httpserver.WriteError` on failure, store `claims.UserID` under unexported `ctxKey` on success.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/modules/auth/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/auth/jwt.go internal/modules/auth/middleware.go internal/modules/auth/jwt_test.go go.mod go.sum
git commit -m "feat(auth): add HS256 JWT manager and RequireAuth middleware"
```

---

### Task 5: Auth Domain, Repository, Service & HTTP Handlers

**Files:**
- Create: `internal/modules/auth/model.go`
- Create: `internal/modules/auth/repository.go`
- Create: `internal/modules/auth/service.go`
- Create: `internal/modules/auth/handler.go`
- Test: `internal/modules/auth/service_test.go`
- Test: `internal/modules/auth/handler_test.go`

**Interfaces:**
- Consumes: `httpserver.WriteJSON`, `httpserver.WriteError`, `httpserver.DecodeAndValidate`, `auth.TokenManager`, `auth.RequireAuth`, `auth.UserIDFromContext`
- Produces:
  - `auth.NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository`
  - `auth.NewService(repo Repository, tm *TokenManager) *Service`
  - `auth.NewHandler(svc *Service) *Handler`
  - `(*Handler).RegisterRoutes(r chi.Router, tm *TokenManager)`

- [ ] **Step 1: Install `golang.org/x/crypto` and write failing service & handler tests**

Run: `go get golang.org/x/crypto`

Create `internal/modules/auth/service_test.go` (using an in-memory mock `Repository`) covering:
- `TestService_Register_SuccessAndDuplicate`: verifies password is hashed (not plaintext), JWT returned, duplicate email returns `auth.ErrEmailAlreadyExists`.
- `TestService_Login_ValidInvalidInactive`: verifies valid login returns token, wrong password returns `auth.ErrInvalidCredentials`, `status != "active"` returns `auth.ErrUserInactive`.

Create `internal/modules/auth/handler_test.go` (using `chi.NewRouter()` + `httptest`) covering:
- `POST /api/v1/auth/register` -> `201` (response JSON does NOT contain `"password_hash"`), duplicate -> `409`, invalid email/short password -> `400`.
- `POST /api/v1/auth/login` -> `200` with token, wrong password -> `401`.
- `GET /api/v1/auth/me` -> `401` without token, `200` with token returning `id`, `email`, `status`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/modules/auth/...`
Expected: FAIL (`Service`, `Handler`, `Repository` not defined).

- [ ] **Step 3: Implement `model.go`, `repository.go`, `service.go`, `handler.go`**

- `model.go`: `User` (`PasswordHash string` tagged `json:"-"`), `RegisterRequest` (`Email` `validate:"required,email"`, `Password` `validate:"required,min=8,max=72"`), `LoginRequest`, `UserResponse`, `AuthResponse`.
- `repository.go`: `PostgresRepository` with `CreateUser`, `GetUserByEmail`, `GetUserByID`; map `pgconn.PgError` code `23505` to `ErrEmailAlreadyExists` and `pgx.ErrNoRows` to `ErrUserNotFound`.
- `service.go`: Normalize email (`strings.TrimSpace(strings.ToLower(email))`), `bcrypt.GenerateFromPassword(..., bcrypt.DefaultCost)`, `bcrypt.CompareHashAndPassword`.
- `handler.go`: `RegisterRoutes(r chi.Router, tm *TokenManager)` mounting `/register`, `/login`, and protected `/me` under `/api/v1/auth`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/modules/auth/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/auth go.mod go.sum
git commit -m "feat(auth): implement user registration, login, and /me endpoint"
```

---

### Task 6: Wire Entrypoint (`cmd/api/main.go`) & End-to-End Integration Tests

**Files:**
- Modify: `cmd/api/main.go`
- Create: `tests/integration/auth_integration_test.go`

**Interfaces:**
- Consumes: `config.Load`, `logger.New`, `database.NewPostgresPool`, `database.MigrateUp`, `migrations.FS`, `httpserver.NewRouter`, `auth.NewPostgresRepository`, `auth.NewTokenManager`, `auth.NewService`, `auth.NewHandler`

- [ ] **Step 1: Write integration test `tests/integration/auth_integration_test.go`**

- Reads `TEST_DATABASE_URL` (fallback `postgres://fincore_app:local_dev_password@localhost:5432/fincore?sslmode=disable`).
- Attempts `database.NewPostgresPool(ctx, dsn)` with 2s timeout; if unreachable, calls `t.Skip("postgres unreachable; skipping integration test")`.
- Runs `database.MigrateUp(dsn, migrations.FS)`, wires `httpserver.NewRouter` + `authHandler.RegisterRoutes`, cleans test user row via `t.Cleanup`.
- Exercises: `GET /healthz` (`200`) -> `POST /api/v1/auth/register` (`201`) -> duplicate `register` (`409`) -> `POST /api/v1/auth/login` (`200`) -> `GET /api/v1/auth/me` (`200` with token, `401` without token).

- [ ] **Step 2: Wire `cmd/api/main.go` with graceful shutdown**

- Load `config.Load()`, init `logger.New`, connect `database.NewPostgresPool` (`defer pool.Close()`), run `database.MigrateUp` if `cfg.AutoMigrate`, wire `auth` + `httpserver.NewRouter`, configure `http.Server` (`ReadHeaderTimeout: 5*time.Second`, `ReadTimeout: 15*time.Second`, `WriteTimeout: 15*time.Second`, `IdleTimeout: 60*time.Second`), listen for `os.Interrupt`/`syscall.SIGTERM`, call `srv.Shutdown` with 10s timeout.

- [ ] **Step 3: Run `go mod tidy`, build, unit tests, and integration tests**

Run: `go mod tidy && go build ./... && go test -race ./...`
Expected: `go build ./...` exits 0; all unit tests PASS; `tests/integration` PASSES if Postgres is running or skips cleanly if not.

- [ ] **Step 4: Commit**

```bash
git add cmd/api/main.go tests/integration/auth_integration_test.go go.mod go.sum
git commit -m "feat(api): wire M1 server with graceful shutdown and auth integration tests"
```
