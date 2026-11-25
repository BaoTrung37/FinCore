package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestAuthFlow_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://fincore_app:local_dev_password@localhost:5432/fincore?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := database.NewPostgresPool(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unreachable; skipping integration test: %v", err)
	}
	defer pool.Close()

	if err := database.MigrateUp(dsn, migrations.FS); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	email := fmt.Sprintf("inttest-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE email = $1", email)
	})

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tm := auth.NewTokenManager("integration-test-secret-32-bytes!!", time.Hour)
	repo := auth.NewPostgresRepository(pool)
	svc := auth.NewService(repo, tm)
	h := auth.NewHandler(svc, log)

	router := httpserver.NewRouter(log, pool)
	h.RegisterRoutes(router, tm)

	// 1. /healthz
	recHealth := httptest.NewRecorder()
	router.ServeHTTP(recHealth, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", recHealth.Code)
	}

	// 2. Register
	body := fmt.Sprintf(`{"email":%q,"password":"password123"}`, email)
	recReg := httptest.NewRecorder()
	router.ServeHTTP(recReg, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body)))
	if recReg.Code != http.StatusCreated {
		t.Fatalf("expected 201 on register, got %d: %s", recReg.Code, recReg.Body.String())
	}

	// 3. Duplicate register -> 409
	recDup := httptest.NewRecorder()
	router.ServeHTTP(recDup, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body)))
	if recDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate register, got %d", recDup.Code)
	}

	// 4. Login -> 200
	recLogin := httptest.NewRecorder()
	router.ServeHTTP(recLogin, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body)))
	if recLogin.Code != http.StatusOK {
		t.Fatalf("expected 200 on login, got %d", recLogin.Code)
	}
	var loginRes auth.AuthResponse
	if err := json.Unmarshal(recLogin.Body.Bytes(), &loginRes); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}

	// 5. /me -> 200 with token, 401 without
	recNoAuth := httptest.NewRecorder()
	router.ServeHTTP(recNoAuth, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", recNoAuth.Code)
	}

	reqMe := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	reqMe.Header.Set("Authorization", "Bearer "+loginRes.AccessToken)
	recMe := httptest.NewRecorder()
	router.ServeHTTP(recMe, reqMe)
	if recMe.Code != http.StatusOK {
		t.Fatalf("expected 200 on /me, got %d: %s", recMe.Code, recMe.Body.String())
	}
}
