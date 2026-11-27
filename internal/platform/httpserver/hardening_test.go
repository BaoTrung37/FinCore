package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/platform/metrics"
)

type okHealthChecker struct{}

func (okHealthChecker) Ping(_ context.Context) error { return nil }

func TestCORS_PreflightAndAllowedOrigin(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouterWithOptions(log, okHealthChecker{}, RouterOptions{
		CORSAllowedOrigins: []string{"https://app.fincore.dev"},
	})

	req := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	req.Header.Set("Origin", "https://app.fincore.dev")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 preflight, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.fincore.dev" {
		t.Fatalf("expected allowed origin header, got %q", got)
	}

	reqDenied := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	reqDenied.Header.Set("Origin", "https://evil.example.com")
	recDenied := httptest.NewRecorder()
	r.ServeHTTP(recDenied, reqDenied)
	if got := recDenied.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected empty CORS origin for disallowed origin, got %q", got)
	}
}

func TestRateLimit_EnforcesBurstAndExemptsHealthz(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouterWithOptions(log, okHealthChecker{}, RouterOptions{
		RateLimitRPS:   1,
		RateLimitBurst: 2,
		Metrics:        metrics.NewRegistry(),
	})
	r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"pong": "ok"})
	})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, rec.Code)
		}
	}

	reqLimited := httptest.NewRequest(http.MethodGet, "/ping", nil)
	reqLimited.RemoteAddr = "10.0.0.1:12345"
	recLimited := httptest.NewRecorder()
	r.ServeHTTP(recLimited, reqLimited)
	if recLimited.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", recLimited.Code)
	}
	if recLimited.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429")
	}

	// /healthz and /metrics must remain exempt.
	for _, path := range []string{"/healthz", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected %s to bypass rate limiter with 200, got %d", path, rec.Code)
		}
	}
}

func TestMetricsEndpoint_RecordsRequests(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := metrics.NewRegistry()
	r := NewRouterWithOptions(log, okHealthChecker{}, RouterOptions{Metrics: reg})

	recHealth := httptest.NewRecorder()
	r.ServeHTTP(recHealth, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", recHealth.Code)
	}

	recMetrics := httptest.NewRecorder()
	r.ServeHTTP(recMetrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recMetrics.Code != http.StatusOK {
		t.Fatalf("expected 200 on /metrics, got %d", recMetrics.Code)
	}
	body := recMetrics.Body.String()
	if !strings.Contains(body, `fincore_http_requests_total{method="GET",route="/healthz",status="200"} 1`) {
		t.Fatalf("expected /healthz metric in /metrics output, got:\n%s", body)
	}
}
