package reconciliation

import (
	"bytes"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/go-chi/chi/v5"
)

func newTestHandlerRouter(t *testing.T) (chi.Router, string) {
	t.Helper()
	tm := auth.NewTokenManager("test-secret-key-at-least-32-chars-long", time.Hour)
	token, err := tm.Generate("00000000-0000-0000-0000-000000000099", "recon@example.com")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	r := chi.NewRouter()
	h := NewHandler(NewService(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.RegisterRoutes(r, tm)
	return r, token
}

func TestHandler_Unauthorized(t *testing.T) {
	r, _ := newTestHandlerRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reconciliation/runs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandler_CreateRun_ValidationError(t *testing.T) {
	r, token := newTestHandlerRouter(t)

	cases := []struct {
		name string
		body string
	}{
		{
			name: "invalid JSON",
			body: "{bad",
		},
		{
			name: "period_end before period_start",
			body: `{"bank_code":"VCB","currency":"VND","period_start":"2026-11-27T10:00:00Z","period_end":"2026-11-27T09:00:00Z","records":[]}`,
		},
		{
			name: "unsupported currency",
			body: `{"bank_code":"VCB","currency":"EUR","period_start":"2026-11-27T08:00:00Z","period_end":"2026-11-27T10:00:00Z","records":[]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/reconciliation/runs", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandler_CreateRunCSV_ValidationError(t *testing.T) {
	r, token := newTestHandlerRouter(t)

	t.Run("missing file field", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("bank_code", "VCB")
		_ = mw.WriteField("currency", "VND")
		_ = mw.WriteField("period_start", "2026-11-27T08:00:00Z")
		_ = mw.WriteField("period_end", "2026-11-27T10:00:00Z")
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/reconciliation/runs/csv", &buf)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("malformed CSV header", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("bank_code", "VCB")
		_ = mw.WriteField("currency", "VND")
		_ = mw.WriteField("period_start", "2026-11-27T08:00:00Z")
		_ = mw.WriteField("period_end", "2026-11-27T10:00:00Z")
		part, _ := mw.CreateFormFile("file", "statement.csv")
		_, _ = part.Write([]byte("wrong,header\n1,2\n"))
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/reconciliation/runs/csv", &buf)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("oversized payload rejected with 413", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("bank_code", "VCB")
		_ = mw.WriteField("currency", "VND")
		_ = mw.WriteField("period_start", "2026-11-27T08:00:00Z")
		_ = mw.WriteField("period_end", "2026-11-27T10:00:00Z")
		part, _ := mw.CreateFormFile("file", "big.csv")
		_, _ = part.Write(bytes.Repeat([]byte("a"), (1<<20)+1024))
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/reconciliation/runs/csv", &buf)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestHandler_ResolveItem_ValidationError(t *testing.T) {
	r, token := newTestHandlerRouter(t)

	cases := []struct {
		name string
		body string
	}{
		{
			name: "invalid status",
			body: `{"resolution_status":"open","resolution_note":"some note"}`,
		},
		{
			name: "empty note",
			body: `{"resolution_status":"resolved","resolution_note":""}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/reconciliation/runs/run-1/items/item-1", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
