package transfer_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/transfer"
	"github.com/go-chi/chi/v5"
)

func TestHandler_ValidationAndAuth(t *testing.T) {
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := transfer.NewService(nil, nil, nil)
	h := transfer.NewHandler(svc, log)

	r := chi.NewRouter()
	h.RegisterRoutes(r, tm)

	tok, _ := tm.Generate("11111111-1111-1111-1111-111111111111", "u@example.com")

	// 1. Unauthenticated -> 401
	recUnauth := httptest.NewRecorder()
	r.ServeHTTP(recUnauth, httptest.NewRequest(http.MethodPost, "/api/v1/transfers", nil))
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recUnauth.Code)
	}

	// 2. Missing Idempotency-Key -> 400
	body := `{"source_account_id":"11111111-1111-1111-1111-111111111111","destination_account_id":"22222222-2222-2222-2222-222222222222","amount":1000,"currency":"VND"}`
	reqNoKey := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(body))
	reqNoKey.Header.Set("Authorization", "Bearer "+tok)
	recNoKey := httptest.NewRecorder()
	r.ServeHTTP(recNoKey, reqNoKey)
	if recNoKey.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing Idempotency-Key, got %d", recNoKey.Code)
	}

	// 3. Self transfer -> 400
	selfBody := `{"source_account_id":"11111111-1111-1111-1111-111111111111","destination_account_id":"11111111-1111-1111-1111-111111111111","amount":1000,"currency":"VND"}`
	reqSelf := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(selfBody))
	reqSelf.Header.Set("Authorization", "Bearer "+tok)
	reqSelf.Header.Set("Idempotency-Key", "idem-1")
	recSelf := httptest.NewRecorder()
	r.ServeHTTP(recSelf, reqSelf)
	if recSelf.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for self transfer, got %d", recSelf.Code)
	}
}
