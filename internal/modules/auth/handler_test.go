package auth_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/go-chi/chi/v5"
)

func setupAuthRouter() (*chi.Mux, *auth.TokenManager) {
	repo := newMockRepo()
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	svc := auth.NewService(repo, tm)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := auth.NewHandler(svc, log)

	r := chi.NewRouter()
	h.RegisterRoutes(r, tm)
	return r, tm
}

func TestHandler_RegisterLoginAndMe(t *testing.T) {
	r, _ := setupAuthRouter()

	// 1. Register 201
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{"email":"u@example.com","password":"password123"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatal("response must not expose password_hash")
	}

	// 2. Duplicate register 409
	reqDup := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{"email":"u@example.com","password":"password123"}`))
	recDup := httptest.NewRecorder()
	r.ServeHTTP(recDup, reqDup)
	if recDup.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", recDup.Code)
	}

	// 3. Invalid payload 400
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{"email":"bad","password":"123"}`))
	recBad := httptest.NewRecorder()
	r.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recBad.Code)
	}

	// 4. Login 200
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"u@example.com","password":"password123"}`))
	recLogin := httptest.NewRecorder()
	r.ServeHTTP(recLogin, reqLogin)
	if recLogin.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recLogin.Code)
	}
	var loginOut auth.AuthResponse
	if err := json.Unmarshal(recLogin.Body.Bytes(), &loginOut); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}

	// 5. /me 200 with token, 401 without
	recMeNoAuth := httptest.NewRecorder()
	r.ServeHTTP(recMeNoAuth, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	if recMeNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recMeNoAuth.Code)
	}

	reqMe := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	reqMe.Header.Set("Authorization", "Bearer "+loginOut.AccessToken)
	recMe := httptest.NewRecorder()
	r.ServeHTTP(recMe, reqMe)
	if recMe.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recMe.Code)
	}
}
