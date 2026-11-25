package account_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/go-chi/chi/v5"
)

func setupAccountRouter() (*chi.Mux, string, string) {
	q := newMockQuerier()
	svc := account.NewService(q)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := account.NewHandler(svc, log)
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)

	r := chi.NewRouter()
	h.RegisterRoutes(r, tm)

	tok1, _ := tm.Generate("11111111-1111-1111-1111-111111111111", "u1@example.com")
	tok2, _ := tm.Generate("22222222-2222-2222-2222-222222222222", "u2@example.com")
	return r, tok1, tok2
}

func TestHandler_AccountLifecycleAndOwnership(t *testing.T) {
	r, tok1, tok2 := setupAccountRouter()

	// 1. Unauthenticated -> 401
	recUnauth := httptest.NewRecorder()
	r.ServeHTTP(recUnauth, httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil))
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recUnauth.Code)
	}

	// 2. Create account -> 201
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", strings.NewReader(`{"currency":"VND"}`))
	reqCreate.Header.Set("Authorization", "Bearer "+tok1)
	recCreate := httptest.NewRecorder()
	r.ServeHTTP(recCreate, reqCreate)
	if recCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recCreate.Code, recCreate.Body.String())
	}
	var created accountdb.Account
	if err := json.Unmarshal(recCreate.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}

	// 3. Cross-user access -> 403
	reqOther := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+created.ID, nil)
	reqOther.Header.Set("Authorization", "Bearer "+tok2)
	recOther := httptest.NewRecorder()
	r.ServeHTTP(recOther, reqOther)
	if recOther.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner, got %d", recOther.Code)
	}

	// 4. Freeze -> 200
	reqFreeze := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+created.ID+"/freeze", nil)
	reqFreeze.Header.Set("Authorization", "Bearer "+tok1)
	recFreeze := httptest.NewRecorder()
	r.ServeHTTP(recFreeze, reqFreeze)
	if recFreeze.Code != http.StatusOK {
		t.Fatalf("expected 200 on freeze, got %d", recFreeze.Code)
	}

	// 5. Balance -> 200
	reqBal := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+created.ID+"/balance", nil)
	reqBal.Header.Set("Authorization", "Bearer "+tok1)
	recBal := httptest.NewRecorder()
	r.ServeHTTP(recBal, reqBal)
	if recBal.Code != http.StatusOK {
		t.Fatalf("expected 200 on balance, got %d", recBal.Code)
	}
}
