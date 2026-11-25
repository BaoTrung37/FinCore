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
