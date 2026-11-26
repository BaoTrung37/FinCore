package payment_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/payment"
	"github.com/go-chi/chi/v5"
)

func TestHandler_AuthValidationAndCallbackSignature(t *testing.T) {
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := payment.NewService(nil, nil, nil, payment.NewMockBankGateway())
	h := payment.NewHandler(svc, log, "webhook-secret-123")

	r := chi.NewRouter()
	h.RegisterRoutes(r, tm)

	tok, _ := tm.Generate("11111111-1111-1111-1111-111111111111", "u@example.com")

	// 1. Unauthenticated deposit -> 401
	recUnauth := httptest.NewRecorder()
	r.ServeHTTP(recUnauth, httptest.NewRequest(http.MethodPost, "/api/v1/payments/deposits", nil))
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recUnauth.Code)
	}

	// 2. Missing Idempotency-Key -> 400
	body := `{"account_id":"11111111-1111-1111-1111-111111111111","amount":10000,"currency":"VND","bank_code":"MOCK_VCB","bank_account_number":"123456789"}`
	reqNoKey := httptest.NewRequest(http.MethodPost, "/api/v1/payments/deposits", strings.NewReader(body))
	reqNoKey.Header.Set("Authorization", "Bearer "+tok)
	recNoKey := httptest.NewRecorder()
	r.ServeHTTP(recNoKey, reqNoKey)
	if recNoKey.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing Idempotency-Key, got %d", recNoKey.Code)
	}

	// 3. Invalid callback signature -> 401
	cbBody := `{"external_ref":"MDEP-123","status":"completed"}`
	reqBadSig := httptest.NewRequest(http.MethodPost, "/api/v1/payments/callbacks/mock-bank", strings.NewReader(cbBody))
	reqBadSig.Header.Set("X-Mock-Bank-Signature", "invalid-signature")
	recBadSig := httptest.NewRecorder()
	r.ServeHTTP(recBadSig, reqBadSig)
	if recBadSig.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid callback signature, got %d", recBadSig.Code)
	}
}
