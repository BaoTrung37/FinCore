package transfer

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/go-chi/chi/v5"
)

type createTransferRequest struct {
	SourceAccountID      string `json:"source_account_id" validate:"required,uuid"`
	DestinationAccountID string `json:"destination_account_id" validate:"required,uuid"`
	Amount               int64  `json:"amount" validate:"required,gt=0"`
	Currency             string `json:"currency" validate:"required,len=3"`
}

// Handler exposes HTTP endpoints for the transfer module.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler constructs a transfer Handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts protected transfer routes under /api/v1/transfers.
func (h *Handler) RegisterRoutes(r chi.Router, tm *auth.TokenManager) {
	r.Route("/api/v1/transfers", func(r chi.Router) {
		r.Use(auth.RequireAuth(tm))
		r.Post("/", h.create)
		r.Get("/{id}", h.get)
	})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required")
		return
	}

	var req createTransferRequest
	if err := httpserver.DecodeAndValidate(w, r, &req); err != nil {
		if errors.Is(err, httpserver.ErrRequestTooLarge) {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error())
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	res, err := h.svc.CreateTransfer(r.Context(), CreateTransferInput{
		UserID:               userID,
		IdempotencyKey:       idemKey,
		SourceAccountID:      req.SourceAccountID,
		DestinationAccountID: req.DestinationAccountID,
		Amount:               req.Amount,
		Currency:             req.Currency,
	})
	if err != nil {
		h.handleCreateError(w, err)
		return
	}

	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	httpserver.WriteJSON(w, status, res.Transfer)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	tr, err := h.svc.GetTransfer(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		switch {
		case errors.Is(err, ErrTransferNotFound):
			httpserver.WriteError(w, http.StatusNotFound, "TRANSFER_NOT_FOUND", "transfer not found")
		case errors.Is(err, ErrForbidden):
			httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not have access to this transfer")
		default:
			h.log.Error("get transfer failed", slog.Any("error", err))
			httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load transfer")
		}
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, tr)
}

func (h *Handler) handleCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrMissingIdempotencyKey),
		errors.Is(err, ErrSelfTransfer),
		errors.Is(err, ErrInvalidAmount),
		errors.Is(err, ErrUnsupportedCurrency):
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, ErrIdempotencyConflict):
		httpserver.WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", err.Error())
	case errors.Is(err, account.ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not own the source account")
	case errors.Is(err, account.ErrAccountNotFound), errors.Is(err, ledger.ErrAccountNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
	case errors.Is(err, ledger.ErrAccountFrozen), errors.Is(err, ledger.ErrAccountClosed):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, "ACCOUNT_UNAVAILABLE", err.Error())
	case errors.Is(err, ledger.ErrInsufficientBalance):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, "INSUFFICIENT_BALANCE", err.Error())
	case errors.Is(err, ledger.ErrCurrencyMismatch):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, "CURRENCY_MISMATCH", err.Error())
	default:
		h.log.Error("create transfer failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to process transfer")
	}
}
