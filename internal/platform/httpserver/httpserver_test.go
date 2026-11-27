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
	rec := httptest.NewRecorder()
	var dst reqBody
	err := httpserver.DecodeAndValidate(rec, req, &dst)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if strings.Contains(err.Error(), "reqBody") {
		t.Fatalf("error must not leak Go struct name: %v", err)
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

func TestSwagger_Endpoints(t *testing.T) {
	r := httpserver.NewRouter(slog.New(slog.NewJSONHandler(io.Discard, nil)), stubHealth{})

	recSpec := httptest.NewRecorder()
	r.ServeHTTP(recSpec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if recSpec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /openapi.json, got %d", recSpec.Code)
	}
	var spec map[string]any
	if err := json.Unmarshal(recSpec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("invalid openapi.json: %v", err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("unexpected openapi version: %v", spec["openapi"])
	}

	recUI := httptest.NewRecorder()
	r.ServeHTTP(recUI, httptest.NewRequest(http.MethodGet, "/swagger", nil))
	if recUI.Code != http.StatusOK || !strings.Contains(recUI.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("expected Swagger UI HTML, got code=%d", recUI.Code)
	}
}
