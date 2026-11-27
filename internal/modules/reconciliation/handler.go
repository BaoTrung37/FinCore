package reconciliation

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	reconciliationdb "github.com/BaoTrung37/FinCore/internal/modules/reconciliation/db"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const maxCSVUploadBytes = 1 << 20 // 1 MiB

type createRunRequest struct {
	BankCode    string                `json:"bank_code" validate:"required"`
	Currency    string                `json:"currency" validate:"required"`
	PeriodStart time.Time             `json:"period_start" validate:"required"`
	PeriodEnd   time.Time             `json:"period_end" validate:"required"`
	Records     []BankStatementRecord `json:"records"`
}

type resolveItemRequest struct {
	ResolutionStatus string `json:"resolution_status" validate:"required,oneof=resolved ignored"`
	ResolutionNote   string `json:"resolution_note" validate:"required"`
}

type runResponse struct {
	ID                   string    `json:"id"`
	BankCode             string    `json:"bank_code"`
	Currency             string    `json:"currency"`
	PeriodStart          time.Time `json:"period_start"`
	PeriodEnd            time.Time `json:"period_end"`
	Status               string    `json:"status"`
	TotalBankRecords     int32     `json:"total_bank_records"`
	TotalInternalRecords int32     `json:"total_internal_records"`
	MatchedCount         int32     `json:"matched_count"`
	DiscrepancyCount     int32     `json:"discrepancy_count"`
	CreatedAt            time.Time `json:"created_at"`
}

type runDetailResponse struct {
	runResponse
	Items []itemResponse `json:"items"`
}

type itemResponse struct {
	ID                string     `json:"id"`
	RunID             string     `json:"run_id"`
	ExternalRef       string     `json:"external_ref"`
	PaymentID         *string    `json:"payment_id,omitempty"`
	MatchStatus       string     `json:"match_status"`
	BankAmount        *int64     `json:"bank_amount,omitempty"`
	InternalAmount    *int64     `json:"internal_amount,omitempty"`
	BankDirection     string     `json:"bank_direction,omitempty"`
	InternalDirection string     `json:"internal_direction,omitempty"`
	BankStatus        string     `json:"bank_status,omitempty"`
	InternalStatus    string     `json:"internal_status,omitempty"`
	Currency          string     `json:"currency"`
	ResolutionStatus  string     `json:"resolution_status"`
	ResolutionNote    string     `json:"resolution_note,omitempty"`
	ResolvedBy        *string    `json:"resolved_by,omitempty"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

type ledgerAuditResponse struct {
	ID                     string          `json:"id"`
	Status                 string          `json:"status"`
	CheckedTransactions    int32           `json:"checked_transactions"`
	UnbalancedTransactions int32           `json:"unbalanced_transactions"`
	CheckedAccounts        int32           `json:"checked_accounts"`
	DriftedAccounts        int32           `json:"drifted_accounts"`
	Anomalies              json.RawMessage `json:"anomalies"`
	CreatedAt              time.Time       `json:"created_at"`
}

// Handler exposes HTTP endpoints for bank statement reconciliation and ledger audits.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler constructs a reconciliation Handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts authenticated reconciliation routes under /api/v1/reconciliation.
func (h *Handler) RegisterRoutes(r chi.Router, tm *auth.TokenManager) {
	r.Route("/api/v1/reconciliation", func(r chi.Router) {
		r.Use(auth.RequireAuth(tm))
		r.Post("/runs", h.createRun)
		r.Post("/runs/csv", h.createRunCSV)
		r.Get("/runs", h.listRuns)
		r.Get("/runs/{id}", h.getRun)
		r.Patch("/runs/{id}/items/{item_id}", h.resolveItem)
		r.Post("/ledger-audit", h.runLedgerAudit)
		r.Get("/ledger-audit", h.listLedgerAudits)
	})
}

func (h *Handler) createRun(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	var req createRunRequest
	if err := httpserver.DecodeAndValidate(w, r, &req); err != nil {
		if errors.Is(err, httpserver.ErrRequestTooLarge) {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error())
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	res, err := h.svc.CreateRun(r.Context(), CreateRunInput{
		UserID:      userID,
		BankCode:    req.BankCode,
		Currency:    req.Currency,
		PeriodStart: req.PeriodStart,
		PeriodEnd:   req.PeriodEnd,
		Records:     req.Records,
	})
	if err != nil {
		h.handleServiceError(w, err, "create reconciliation run failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, toRunDetailResponse(res.Run, res.Items))
}

func (h *Handler) createRunCSV(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCSVUploadBytes)
	if err := r.ParseMultipartForm(maxCSVUploadBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) || strings.Contains(err.Error(), "request body too large") {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds 1 MiB limit")
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid multipart form data")
		return
	}

	bankCode := strings.TrimSpace(r.FormValue("bank_code"))
	if bankCode == "" {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", ErrInvalidBankCode.Error())
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	if currency != "VND" && currency != "USD" {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", ErrUnsupportedCurrency.Error())
		return
	}
	periodStart, err := time.Parse(time.RFC3339, strings.TrimSpace(r.FormValue("period_start")))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "period_start must be valid RFC3339 timestamp")
		return
	}
	periodEnd, err := time.Parse(time.RFC3339, strings.TrimSpace(r.FormValue("period_end")))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "period_end must be valid RFC3339 timestamp")
		return
	}
	if periodEnd.Before(periodStart) {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", ErrInvalidPeriod.Error())
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "file field is required")
		return
	}
	defer func() { _ = file.Close() }()

	records, err := ParseStatementCSV(file)
	if err != nil {
		h.handleServiceError(w, err, "parse CSV statement failed")
		return
	}

	res, err := h.svc.CreateRun(r.Context(), CreateRunInput{
		UserID:      userID,
		BankCode:    bankCode,
		Currency:    currency,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Records:     records,
	})
	if err != nil {
		h.handleServiceError(w, err, "create CSV reconciliation run failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, toRunDetailResponse(res.Run, res.Items))
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	limit, offset := parsePagination(r)
	runs, err := h.svc.ListRuns(r.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("list reconciliation runs failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list reconciliation runs")
		return
	}

	resp := make([]runResponse, 0, len(runs))
	for _, run := range runs {
		resp = append(resp, toRunResponse(run))
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	res, err := h.svc.GetRunWithItems(
		r.Context(),
		userID,
		chi.URLParam(r, "id"),
		r.URL.Query().Get("match_status"),
		r.URL.Query().Get("resolution_status"),
	)
	if err != nil {
		h.handleServiceError(w, err, "get reconciliation run failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, toRunDetailResponse(res.Run, res.Items))
}

func (h *Handler) resolveItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	var req resolveItemRequest
	if err := httpserver.DecodeAndValidate(w, r, &req); err != nil {
		if errors.Is(err, httpserver.ErrRequestTooLarge) {
			httpserver.WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", err.Error())
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	updated, err := h.svc.ResolveItem(r.Context(), ResolveItemInput{
		UserID:           userID,
		RunID:            chi.URLParam(r, "id"),
		ItemID:           chi.URLParam(r, "item_id"),
		ResolutionStatus: req.ResolutionStatus,
		ResolutionNote:   req.ResolutionNote,
	})
	if err != nil {
		h.handleServiceError(w, err, "resolve reconciliation item failed")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, toItemResponse(updated))
}

func (h *Handler) runLedgerAudit(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	audit, err := h.svc.RunLedgerAudit(r.Context(), userID)
	if err != nil {
		h.log.Error("run ledger audit failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to run ledger audit")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, toLedgerAuditResponse(audit))
}

func (h *Handler) listLedgerAudits(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	limit, offset := parsePagination(r)
	runs, err := h.svc.ListLedgerAudits(r.Context(), userID, limit, offset)
	if err != nil {
		h.log.Error("list ledger audit runs failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list ledger audit runs")
		return
	}

	resp := make([]ledgerAuditResponse, 0, len(runs))
	for _, a := range runs {
		resp = append(resp, toLedgerAuditResponse(a))
	}
	httpserver.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleServiceError(w http.ResponseWriter, err error, logMsg string) {
	switch {
	case errors.Is(err, ErrInvalidBankCode),
		errors.Is(err, ErrUnsupportedCurrency),
		errors.Is(err, ErrInvalidPeriod):
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, ErrDuplicateExternalRef):
		httpserver.WriteError(w, http.StatusBadRequest, "DUPLICATE_EXTERNAL_REF", err.Error())
	case errors.Is(err, ErrInvalidStatementRecord):
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_STATEMENT_RECORD", err.Error())
	case errors.Is(err, ErrInvalidCSVFormat):
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_CSV_FORMAT", err.Error())
	case errors.Is(err, ErrInvalidResolution):
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_RESOLUTION", err.Error())
	case errors.Is(err, ErrItemNotResolvable):
		httpserver.WriteError(w, http.StatusBadRequest, "ITEM_NOT_RESOLVABLE", err.Error())
	case errors.Is(err, ErrRunNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "RUN_NOT_FOUND", err.Error())
	case errors.Is(err, ErrItemNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "ITEM_NOT_FOUND", err.Error())
	default:
		h.log.Error(logMsg, slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func parsePagination(r *http.Request) (int32, int32) {
	limit := int32(20)
	offset := int32(0)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			limit = int32(v)
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			offset = int32(v)
		}
	}
	return limit, offset
}

func toRunResponse(run reconciliationdb.ReconciliationRun) runResponse {
	return runResponse{
		ID:                   run.ID,
		BankCode:             run.BankCode,
		Currency:             run.Currency,
		PeriodStart:          run.PeriodStart,
		PeriodEnd:            run.PeriodEnd,
		Status:               run.Status,
		TotalBankRecords:     run.TotalBankRecords,
		TotalInternalRecords: run.TotalInternalRecords,
		MatchedCount:         run.MatchedCount,
		DiscrepancyCount:     run.DiscrepancyCount,
		CreatedAt:            run.CreatedAt,
	}
}

func toRunDetailResponse(run reconciliationdb.ReconciliationRun, items []reconciliationdb.ReconciliationItem) runDetailResponse {
	outItems := make([]itemResponse, 0, len(items))
	for _, it := range items {
		outItems = append(outItems, toItemResponse(it))
	}
	return runDetailResponse{
		runResponse: toRunResponse(run),
		Items:       outItems,
	}
}

func toItemResponse(it reconciliationdb.ReconciliationItem) itemResponse {
	resp := itemResponse{
		ID:                it.ID,
		RunID:             it.RunID,
		ExternalRef:       it.ExternalRef,
		PaymentID:         uuidPtr(it.PaymentID),
		MatchStatus:       it.MatchStatus,
		BankAmount:        int64Ptr(it.BankAmount),
		InternalAmount:    int64Ptr(it.InternalAmount),
		BankDirection:     it.BankDirection,
		InternalDirection: it.InternalDirection,
		BankStatus:        it.BankStatus,
		InternalStatus:    it.InternalStatus,
		Currency:          it.Currency,
		ResolutionStatus:  it.ResolutionStatus,
		ResolutionNote:    it.ResolutionNote,
		ResolvedBy:        uuidPtr(it.ResolvedBy),
		ResolvedAt:        timePtr(it.ResolvedAt),
		CreatedAt:         it.CreatedAt,
	}
	return resp
}

func toLedgerAuditResponse(a reconciliationdb.LedgerAuditRun) ledgerAuditResponse {
	anomalies := json.RawMessage(a.Anomalies)
	if len(anomalies) == 0 {
		anomalies = json.RawMessage("[]")
	}
	return ledgerAuditResponse{
		ID:                     a.ID,
		Status:                 a.Status,
		CheckedTransactions:    a.CheckedTransactions,
		UnbalancedTransactions: a.UnbalancedTransactions,
		CheckedAccounts:        a.CheckedAccounts,
		DriftedAccounts:        a.DriftedAccounts,
		Anomalies:              anomalies,
		CreatedAt:              a.CreatedAt,
	}
}

func uuidPtr(u pgtype.UUID) *string {
	if !u.Valid {
		return nil
	}
	v, err := u.Value()
	if err != nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}

func int64Ptr(n pgtype.Int8) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func timePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}
