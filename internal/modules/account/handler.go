package account

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/go-chi/chi/v5"
)

type createAccountRequest struct {
	Currency string `json:"currency" validate:"required,len=3"`
}

// Handler exposes HTTP endpoints for the account module.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler constructs an account Handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts protected account routes under /api/v1/accounts.
func (h *Handler) RegisterRoutes(r chi.Router, tm *auth.TokenManager) {
	r.Route("/api/v1/accounts", func(r chi.Router) {
		r.Use(auth.RequireAuth(tm))
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Get("/{id}", h.get)
		r.Post("/{id}/freeze", h.freeze)
		r.Post("/{id}/unfreeze", h.unfreeze)
		r.Get("/{id}/balance", h.balance)
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	var req createAccountRequest
	if err := httpserver.DecodeAndValidate(w, r, &req); err != nil {
		if errors.Is(err, httpserver.ErrRequestTooLarge) {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error())
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	acc, err := h.svc.CreateAccount(r.Context(), userID, req.Currency)
	if err != nil {
		if errors.Is(err, ErrUnsupportedCurrency) {
			httpserver.WriteError(w, http.StatusBadRequest, "UNSUPPORTED_CURRENCY", "supported currencies are VND and USD")
			return
		}
		h.log.Error("create account failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create account")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, acc)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	accs, err := h.svc.ListAccounts(r.Context(), userID)
	if err != nil {
		h.log.Error("list accounts failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list accounts")
		return
	}
	if accs == nil {
		accs = []accountdb.Account{}
	}
	httpserver.WriteJSON(w, http.StatusOK, accs)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	acc, err := h.svc.GetAccount(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		h.handleAccountError(w, err, "get account failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, acc)
}

func (h *Handler) freeze(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	acc, err := h.svc.FreezeAccount(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		h.handleAccountError(w, err, "freeze account failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, acc)
}

func (h *Handler) unfreeze(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	acc, err := h.svc.UnfreezeAccount(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		h.handleAccountError(w, err, "unfreeze account failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, acc)
}

func (h *Handler) balance(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	bal, err := h.svc.GetBalance(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		h.handleAccountError(w, err, "get balance failed")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, bal)
}

func (h *Handler) handleAccountError(w http.ResponseWriter, err error, logMsg string) {
	switch {
	case errors.Is(err, ErrAccountNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
	case errors.Is(err, ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not have access to this account")
	case errors.Is(err, ErrAccountClosed):
		httpserver.WriteError(w, http.StatusConflict, "ACCOUNT_CLOSED", "closed account cannot be modified")
	default:
		h.log.Error(logMsg, slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
