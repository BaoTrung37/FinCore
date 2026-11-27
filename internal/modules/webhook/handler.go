package webhook

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	webhookdb "github.com/BaoTrung37/FinCore/internal/modules/webhook/db"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/go-chi/chi/v5"
)

type createWebhookRequest struct {
	URL    string   `json:"url" validate:"required,url"`
	Events []string `json:"events"`
}

type webhookResponse struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Secret    string    `json:"secret,omitempty"`
	Events    []string  `json:"events"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// Handler exposes HTTP endpoints for managing webhook subscriptions.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler constructs a webhook Handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts authenticated webhook management routes under /api/v1/webhooks.
func (h *Handler) RegisterRoutes(r chi.Router, tm *auth.TokenManager) {
	r.Route("/api/v1/webhooks", func(r chi.Router) {
		r.Use(auth.RequireAuth(tm))
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Delete("/{id}", h.delete)
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	var req createWebhookRequest
	if err := httpserver.DecodeAndValidate(w, r, &req); err != nil {
		if errors.Is(err, httpserver.ErrRequestTooLarge) {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error())
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	ep, err := h.svc.CreateEndpoint(r.Context(), userID, req.URL, req.Events)
	if err != nil {
		if errors.Is(err, ErrInvalidURL) {
			httpserver.WriteError(w, http.StatusBadRequest, "INVALID_WEBHOOK_URL", err.Error())
			return
		}
		h.log.Error("create webhook endpoint failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create webhook endpoint")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, toWebhookResponse(ep, true))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	items, err := h.svc.ListEndpoints(r.Context(), userID)
	if err != nil {
		h.log.Error("list webhook endpoints failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list webhook endpoints")
		return
	}

	resp := make([]webhookResponse, 0, len(items))
	for _, ep := range items {
		resp = append(resp, toWebhookResponse(ep, false))
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	if err := h.svc.DeleteEndpoint(r.Context(), userID, chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, ErrWebhookNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "WEBHOOK_NOT_FOUND", "webhook endpoint not found")
			return
		}
		h.log.Error("delete webhook endpoint failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete webhook endpoint")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func toWebhookResponse(ep webhookdb.WebhookEndpoint, includeSecret bool) webhookResponse {
	resp := webhookResponse{
		ID:        ep.ID,
		URL:       ep.Url,
		Events:    ep.Events,
		IsActive:  ep.IsActive,
		CreatedAt: ep.CreatedAt,
	}
	if includeSecret {
		resp.Secret = ep.Secret
	}
	return resp
}
