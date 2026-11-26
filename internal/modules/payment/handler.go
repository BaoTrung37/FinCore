package payment

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	paymentdb "github.com/BaoTrung37/FinCore/internal/modules/payment/db"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/go-chi/chi/v5"
)

type createPaymentRequest struct {
	AccountID         string `json:"account_id" validate:"required,uuid"`
	Amount            int64  `json:"amount" validate:"required,gt=0"`
	Currency          string `json:"currency" validate:"required,len=3"`
	BankCode          string `json:"bank_code" validate:"required,min=2,max=32"`
	BankAccountNumber string `json:"bank_account_number" validate:"required,min=4,max=34"`
}

type bankCallbackRequest struct {
	ExternalRef   string `json:"external_ref"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
}

// Handler exposes HTTP endpoints for the payment module.
type Handler struct {
	svc           *Service
	log           *slog.Logger
	webhookSecret string
}

// NewHandler constructs a payment Handler.
func NewHandler(svc *Service, log *slog.Logger, webhookSecret string) *Handler {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Handler{
		svc:           svc,
		log:           log,
		webhookSecret: webhookSecret,
	}
}

// RegisterRoutes mounts payment routes under /api/v1/payments.
func (h *Handler) RegisterRoutes(r chi.Router, tm *auth.TokenManager) {
	r.Route("/api/v1/payments", func(r chi.Router) {
		r.Post("/callbacks/mock-bank", h.mockBankCallback)

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth(tm))
			r.Post("/deposits", h.createDeposit)
			r.Post("/withdrawals", h.createWithdrawal)
			r.Get("/", h.list)
			r.Get("/{id}", h.get)
		})
	})
}

func (h *Handler) createDeposit(w http.ResponseWriter, r *http.Request) {
	h.handleInitiate(w, r, "deposit")
}

func (h *Handler) createWithdrawal(w http.ResponseWriter, r *http.Request) {
	h.handleInitiate(w, r, "withdrawal")
}

func (h *Handler) handleInitiate(w http.ResponseWriter, r *http.Request, direction string) {
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

	var req createPaymentRequest
	if err := httpserver.DecodeAndValidate(w, r, &req); err != nil {
		if errors.Is(err, httpserver.ErrRequestTooLarge) {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error())
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	in := CreatePaymentInput{
		UserID:            userID,
		AccountID:         req.AccountID,
		IdempotencyKey:    idemKey,
		Amount:            req.Amount,
		Currency:          req.Currency,
		BankCode:          req.BankCode,
		BankAccountNumber: req.BankAccountNumber,
	}

	var (
		res CreatePaymentResult
		err error
	)
	if direction == "deposit" {
		res, err = h.svc.InitiateDeposit(r.Context(), in)
	} else {
		res, err = h.svc.InitiateWithdrawal(r.Context(), in)
	}
	if err != nil {
		h.handlePaymentError(w, err)
		return
	}

	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	httpserver.WriteJSON(w, status, res.Payment)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	limit := parseInt32Query(r.URL.Query().Get("limit"), 20)
	offset := parseInt32Query(r.URL.Query().Get("offset"), 0)

	items, err := h.svc.ListPayments(r.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("list payments failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list payments")
		return
	}
	if items == nil {
		items = []paymentdb.Payment{}
	}
	httpserver.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	p, err := h.svc.GetPayment(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		switch {
		case errors.Is(err, ErrPaymentNotFound):
			httpserver.WriteError(w, http.StatusNotFound, "PAYMENT_NOT_FOUND", "payment not found")
		case errors.Is(err, ErrForbidden):
			httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not have access to this payment")
		default:
			h.log.Error("get payment failed", slog.Any("error", err))
			httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load payment")
		}
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) mockBankCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "failed to read callback body")
		return
	}

	if !VerifyCallbackSignature(h.webhookSecret, body, r.Header.Get("X-Mock-Bank-Signature")) {
		httpserver.WriteError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "invalid mock bank callback signature")
		return
	}

	var req bankCallbackRequest
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "malformed JSON callback payload")
		return
	}

	p, err := h.svc.HandleBankCallback(r.Context(), BankCallbackInput{
		ExternalRef:   req.ExternalRef,
		Status:        req.Status,
		FailureReason: req.FailureReason,
	})
	if err != nil {
		h.handlePaymentError(w, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, p)
}

func (h *Handler) handlePaymentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrMissingIdempotencyKey),
		errors.Is(err, ErrInvalidAmount),
		errors.Is(err, ErrUnsupportedCurrency),
		errors.Is(err, ErrInvalidBankDetails),
		errors.Is(err, ErrInvalidCallbackStatus):
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, ErrIdempotencyConflict):
		httpserver.WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", err.Error())
	case errors.Is(err, account.ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not own the target account")
	case errors.Is(err, account.ErrAccountNotFound), errors.Is(err, ledger.ErrAccountNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
	case errors.Is(err, ErrPaymentNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "PAYMENT_NOT_FOUND", "payment not found")
	case errors.Is(err, ledger.ErrAccountFrozen), errors.Is(err, ledger.ErrAccountClosed):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, "ACCOUNT_UNAVAILABLE", err.Error())
	case errors.Is(err, ledger.ErrInsufficientBalance):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, "INSUFFICIENT_BALANCE", err.Error())
	case errors.Is(err, ledger.ErrCurrencyMismatch):
		httpserver.WriteError(w, http.StatusUnprocessableEntity, "CURRENCY_MISMATCH", err.Error())
	default:
		h.log.Error("payment operation failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to process payment")
	}
}

func parseInt32Query(raw string, fallback int32) int32 {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return int32(v)
}
