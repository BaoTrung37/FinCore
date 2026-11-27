package webhook_test

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
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/webhook"
	webhookdb "github.com/BaoTrung37/FinCore/internal/modules/webhook/db"
	"github.com/go-chi/chi/v5"
)

type mockWebhookQuerier struct {
	endpoints map[string]webhookdb.WebhookEndpoint
	seq       int
}

func newMockWebhookQuerier() *mockWebhookQuerier {
	return &mockWebhookQuerier{endpoints: make(map[string]webhookdb.WebhookEndpoint)}
}

func (m *mockWebhookQuerier) ClaimPendingOutboxEvents(_ context.Context, _ int32) ([]webhookdb.OutboxEvent, error) {
	return nil, nil
}

func (m *mockWebhookQuerier) CreateWebhookEndpoint(_ context.Context, arg webhookdb.CreateWebhookEndpointParams) (webhookdb.WebhookEndpoint, error) {
	m.seq++
	id := "00000000-0000-0000-0000-00000000000" + string(rune('0'+m.seq))
	ep := webhookdb.WebhookEndpoint{
		ID:        id,
		UserID:    arg.UserID,
		Url:       arg.Url,
		Secret:    arg.Secret,
		Events:    arg.Events,
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	m.endpoints[id] = ep
	return ep, nil
}

func (m *mockWebhookQuerier) DeactivateWebhookEndpoint(_ context.Context, arg webhookdb.DeactivateWebhookEndpointParams) (int64, error) {
	ep, ok := m.endpoints[arg.ID]
	if !ok || ep.UserID != arg.UserID || !ep.IsActive {
		return 0, nil
	}
	ep.IsActive = false
	m.endpoints[arg.ID] = ep
	return 1, nil
}

func (m *mockWebhookQuerier) InsertOutboxEvent(_ context.Context, _ webhookdb.InsertOutboxEventParams) (webhookdb.OutboxEvent, error) {
	return webhookdb.OutboxEvent{}, nil
}

func (m *mockWebhookQuerier) ListActiveWebhooksForEvent(_ context.Context, _ webhookdb.ListActiveWebhooksForEventParams) ([]webhookdb.WebhookEndpoint, error) {
	return nil, nil
}

func (m *mockWebhookQuerier) ListWebhookEndpointsByUser(_ context.Context, userID string) ([]webhookdb.WebhookEndpoint, error) {
	var out []webhookdb.WebhookEndpoint
	for _, ep := range m.endpoints {
		if ep.UserID == userID && ep.IsActive {
			out = append(out, ep)
		}
	}
	return out, nil
}

func (m *mockWebhookQuerier) MarkOutboxEventCompleted(_ context.Context, _ string) error {
	return nil
}

func (m *mockWebhookQuerier) MarkOutboxEventFailed(_ context.Context, _ webhookdb.MarkOutboxEventFailedParams) error {
	return nil
}

func (m *mockWebhookQuerier) MarkOutboxEventRetry(_ context.Context, _ webhookdb.MarkOutboxEventRetryParams) error {
	return nil
}

func TestSignature_ComputeAndVerify(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"id":"123","amount":50000}`)

	sig := webhook.ComputeSignature(secret, payload)
	if !webhook.VerifySignature(secret, payload, sig) {
		t.Fatal("expected valid signature to verify")
	}
	if webhook.VerifySignature(secret, payload, "tampered") {
		t.Fatal("expected tampered signature to fail")
	}
	if webhook.VerifySignature("wrong-secret", payload, sig) {
		t.Fatal("expected wrong secret to fail")
	}
}

func TestBackoffDuration(t *testing.T) {
	base := 10 * time.Second
	if got := webhook.BackoffDuration(0, base); got != 10*time.Second {
		t.Fatalf("attempt 0: got %v, want 10s", got)
	}
	if got := webhook.BackoffDuration(1, base); got != 20*time.Second {
		t.Fatalf("attempt 1: got %v, want 20s", got)
	}
	if got := webhook.BackoffDuration(2, base); got != 40*time.Second {
		t.Fatalf("attempt 2: got %v, want 40s", got)
	}
	if got := webhook.BackoffDuration(10, base); got != time.Hour {
		t.Fatalf("attempt 10: got %v, want 1h cap", got)
	}
}

func TestService_EndpointLifecycle(t *testing.T) {
	svc := webhook.NewServiceWithQuerier(newMockWebhookQuerier())
	ctx := context.Background()

	if _, err := svc.CreateEndpoint(ctx, "user-1", "ftp://invalid", nil); !errors.Is(err, webhook.ErrInvalidURL) {
		t.Fatalf("expected ErrInvalidURL, got %v", err)
	}

	ep, err := svc.CreateEndpoint(ctx, "user-1", "https://example.com/hook", []string{"transfer.completed"})
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	if !strings.HasPrefix(ep.Secret, "whsec_") {
		t.Fatalf("expected whsec_ prefix, got %q", ep.Secret)
	}

	list, err := svc.ListEndpoints(ctx, "user-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 endpoint, got %d (err=%v)", len(list), err)
	}

	if err := svc.DeleteEndpoint(ctx, "user-2", ep.ID); !errors.Is(err, webhook.ErrWebhookNotFound) {
		t.Fatalf("expected ErrWebhookNotFound for non-owner, got %v", err)
	}
	if err := svc.DeleteEndpoint(ctx, "user-1", ep.ID); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
}

func TestHandler_WebhookRoutes(t *testing.T) {
	svc := webhook.NewServiceWithQuerier(newMockWebhookQuerier())
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := webhook.NewHandler(svc, log)
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)

	r := chi.NewRouter()
	h.RegisterRoutes(r, tm)

	tok, _ := tm.Generate("11111111-1111-1111-1111-111111111111", "u1@example.com")

	// Create -> 201 with secret
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{"url":"https://example.com/hook","events":["payment.completed"]}`))
	reqCreate.Header.Set("Authorization", "Bearer "+tok)
	recCreate := httptest.NewRecorder()
	r.ServeHTTP(recCreate, reqCreate)
	if recCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recCreate.Code, recCreate.Body.String())
	}

	var created struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(recCreate.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Secret == "" {
		t.Fatal("expected secret on create response")
	}

	// List -> 200 without secret
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/webhooks", nil)
	reqList.Header.Set("Authorization", "Bearer "+tok)
	recList := httptest.NewRecorder()
	r.ServeHTTP(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recList.Code)
	}
	if strings.Contains(recList.Body.String(), created.Secret) {
		t.Fatal("expected list response to omit secret")
	}

	// Delete -> 204
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/v1/webhooks/"+created.ID, nil)
	reqDel.Header.Set("Authorization", "Bearer "+tok)
	recDel := httptest.NewRecorder()
	r.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recDel.Code)
	}
}
