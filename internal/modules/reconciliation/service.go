package reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	reconciliationdb "github.com/BaoTrung37/FinCore/internal/modules/reconciliation/db"
	"github.com/BaoTrung37/FinCore/internal/modules/webhook"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxPublisher persists domain events inside the active database transaction.
type OutboxPublisher interface {
	PublishTx(ctx context.Context, tx pgx.Tx, evt webhook.Event) error
}

// CreateRunInput defines parameters for executing a bank statement reconciliation run.
type CreateRunInput struct {
	UserID      string
	BankCode    string
	Currency    string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Records     []BankStatementRecord
}

// RunWithItems bundles a reconciliation run with its persisted comparison items.
type RunWithItems struct {
	Run   reconciliationdb.ReconciliationRun
	Items []reconciliationdb.ReconciliationItem
}

// ResolveItemInput defines parameters for resolving or ignoring an open discrepancy item.
type ResolveItemInput struct {
	UserID           string
	RunID            string
	ItemID           string
	ResolutionStatus string // "resolved" or "ignored"
	ResolutionNote   string
}

// LedgerAnomaly describes a single integrity violation detected during a ledger audit.
type LedgerAnomaly struct {
	Type            string `json:"type"` // "unbalanced_transaction" or "balance_drift"
	TransactionID   string `json:"transaction_id,omitempty"`
	Reference       string `json:"reference,omitempty"`
	EntryCount      int64  `json:"entry_count,omitempty"`
	CurrencyCount   int64  `json:"currency_count,omitempty"`
	TotalDebit      int64  `json:"total_debit,omitempty"`
	TotalCredit     int64  `json:"total_credit,omitempty"`
	AccountID       string `json:"account_id,omitempty"`
	AccountNumber   string `json:"account_number,omitempty"`
	Currency        string `json:"currency,omitempty"`
	CachedBalance   int64  `json:"cached_balance,omitempty"`
	ComputedBalance int64  `json:"computed_balance,omitempty"`
}

// Service orchestrates external bank reconciliation and internal ledger integrity audits.
type Service struct {
	pool   *pgxpool.Pool
	outbox OutboxPublisher
}

// NewService constructs a reconciliation Service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// SetOutboxPublisher attaches an optional transactional outbox publisher.
func (s *Service) SetOutboxPublisher(pub OutboxPublisher) {
	s.outbox = pub
}

// CreateRun reconciles a bank statement batch against the user's FinCore payments in the given period.
func (s *Service) CreateRun(ctx context.Context, in CreateRunInput) (RunWithItems, error) {
	bankCode := strings.ToUpper(strings.TrimSpace(in.BankCode))
	if bankCode == "" {
		return RunWithItems{}, ErrInvalidBankCode
	}
	curr := strings.ToUpper(strings.TrimSpace(in.Currency))
	if curr != "VND" && curr != "USD" {
		return RunWithItems{}, ErrUnsupportedCurrency
	}
	if in.PeriodStart.IsZero() || in.PeriodEnd.IsZero() || in.PeriodEnd.Before(in.PeriodStart) {
		return RunWithItems{}, ErrInvalidPeriod
	}

	// Validate bank statement records up front before opening a DB transaction.
	if _, _, err := MatchRecords(curr, in.Records, nil); err != nil {
		return RunWithItems{}, err
	}

	if s.pool == nil {
		return RunWithItems{}, errors.New("database pool is not configured")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RunWithItems{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	q := reconciliationdb.New(tx)
	paymentRows, err := q.ListPaymentsForReconciliation(ctx, reconciliationdb.ListPaymentsForReconciliationParams{
		UserID:      in.UserID,
		BankCode:    bankCode,
		Currency:    curr,
		CreatedAt:   in.PeriodStart,
		CreatedAt_2: in.PeriodEnd,
	})
	if err != nil {
		return RunWithItems{}, fmt.Errorf("list payments for reconciliation: %w", err)
	}

	internalPayments := make([]InternalPaymentRecord, 0, len(paymentRows))
	for _, p := range paymentRows {
		internalPayments = append(internalPayments, InternalPaymentRecord{
			PaymentID:   p.ID,
			ExternalRef: p.ExternalRef,
			Direction:   p.Direction,
			Amount:      p.Amount,
			Currency:    p.Currency,
			Status:      p.Status,
		})
	}

	matchedItems, summary, err := MatchRecords(curr, in.Records, internalPayments)
	if err != nil {
		return RunWithItems{}, err
	}

	run, err := q.CreateReconciliationRun(ctx, reconciliationdb.CreateReconciliationRunParams{
		UserID:               in.UserID,
		BankCode:             bankCode,
		Currency:             curr,
		PeriodStart:          in.PeriodStart,
		PeriodEnd:            in.PeriodEnd,
		Status:               summary.Status,
		TotalBankRecords:     summary.TotalBankRecords,
		TotalInternalRecords: summary.TotalInternalRecords,
		MatchedCount:         summary.MatchedCount,
		DiscrepancyCount:     summary.DiscrepancyCount,
	})
	if err != nil {
		return RunWithItems{}, fmt.Errorf("create reconciliation run: %w", err)
	}

	persistedItems := make([]reconciliationdb.ReconciliationItem, 0, len(matchedItems))
	for _, m := range matchedItems {
		item, err := q.CreateReconciliationItem(ctx, reconciliationdb.CreateReconciliationItemParams{
			RunID:             run.ID,
			ExternalRef:       m.ExternalRef,
			PaymentID:         toNullableUUID(m.PaymentID),
			MatchStatus:       m.MatchStatus,
			BankAmount:        toNullableInt8(m.BankAmount),
			InternalAmount:    toNullableInt8(m.InternalAmount),
			BankDirection:     m.BankDirection,
			InternalDirection: m.InternalDirection,
			BankStatus:        m.BankStatus,
			InternalStatus:    m.InternalStatus,
			Currency:          m.Currency,
			ResolutionStatus:  m.ResolutionStatus,
		})
		if err != nil {
			return RunWithItems{}, fmt.Errorf("create reconciliation item: %w", err)
		}
		persistedItems = append(persistedItems, item)
	}

	if s.outbox != nil {
		if err := s.outbox.PublishTx(ctx, tx, webhook.Event{
			UserID:        in.UserID,
			AggregateType: "reconciliation_run",
			AggregateID:   run.ID,
			EventType:     "reconciliation.run_completed",
			Payload: map[string]any{
				"run_id":                 run.ID,
				"bank_code":              run.BankCode,
				"currency":               run.Currency,
				"status":                 run.Status,
				"total_bank_records":     run.TotalBankRecords,
				"total_internal_records": run.TotalInternalRecords,
				"matched_count":          run.MatchedCount,
				"discrepancy_count":      run.DiscrepancyCount,
			},
		}); err != nil {
			return RunWithItems{}, fmt.Errorf("publish reconciliation outbox event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return RunWithItems{}, fmt.Errorf("commit reconciliation run: %w", err)
	}
	return RunWithItems{Run: run, Items: persistedItems}, nil
}

// ListRuns returns paginated reconciliation runs owned by userID.
func (s *Service) ListRuns(ctx context.Context, userID string, limit, offset int32) ([]reconciliationdb.ReconciliationRun, error) {
	if s.pool == nil {
		return nil, errors.New("database pool is not configured")
	}
	limit, offset = normalizePagination(limit, offset)
	runs, err := reconciliationdb.New(s.pool).ListReconciliationRunsByUser(ctx, reconciliationdb.ListReconciliationRunsByUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list reconciliation runs: %w", err)
	}
	if runs == nil {
		return []reconciliationdb.ReconciliationRun{}, nil
	}
	return runs, nil
}

// GetRunWithItems returns a user-owned reconciliation run and its filtered items.
func (s *Service) GetRunWithItems(ctx context.Context, userID, runID, matchStatus, resolutionStatus string) (RunWithItems, error) {
	if s.pool == nil {
		return RunWithItems{}, errors.New("database pool is not configured")
	}
	q := reconciliationdb.New(s.pool)
	run, err := q.GetReconciliationRunByIDAndUser(ctx, reconciliationdb.GetReconciliationRunByIDAndUserParams{
		ID:     runID,
		UserID: userID,
	})
	if err != nil {
		if isNotFoundOrBadUUID(err) {
			return RunWithItems{}, ErrRunNotFound
		}
		return RunWithItems{}, fmt.Errorf("get reconciliation run: %w", err)
	}

	items, err := q.ListReconciliationItemsByRun(ctx, reconciliationdb.ListReconciliationItemsByRunParams{
		RunID:            run.ID,
		MatchStatus:      toNullableText(matchStatus),
		ResolutionStatus: toNullableText(resolutionStatus),
	})
	if err != nil {
		return RunWithItems{}, fmt.Errorf("list reconciliation items: %w", err)
	}
	if items == nil {
		items = []reconciliationdb.ReconciliationItem{}
	}
	return RunWithItems{Run: run, Items: items}, nil
}

// ResolveItem marks an open discrepancy item as resolved or ignored with an audit note.
func (s *Service) ResolveItem(ctx context.Context, in ResolveItemInput) (reconciliationdb.ReconciliationItem, error) {
	status := strings.ToLower(strings.TrimSpace(in.ResolutionStatus))
	note := strings.TrimSpace(in.ResolutionNote)
	if (status != "resolved" && status != "ignored") || note == "" {
		return reconciliationdb.ReconciliationItem{}, ErrInvalidResolution
	}
	if s.pool == nil {
		return reconciliationdb.ReconciliationItem{}, errors.New("database pool is not configured")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return reconciliationdb.ReconciliationItem{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	q := reconciliationdb.New(tx)
	if _, err := q.GetReconciliationRunByIDAndUser(ctx, reconciliationdb.GetReconciliationRunByIDAndUserParams{
		ID:     in.RunID,
		UserID: in.UserID,
	}); err != nil {
		if isNotFoundOrBadUUID(err) {
			return reconciliationdb.ReconciliationItem{}, ErrRunNotFound
		}
		return reconciliationdb.ReconciliationItem{}, fmt.Errorf("get reconciliation run: %w", err)
	}

	item, err := q.GetReconciliationItemByIDAndRun(ctx, reconciliationdb.GetReconciliationItemByIDAndRunParams{
		ID:    in.ItemID,
		RunID: in.RunID,
	})
	if err != nil {
		if isNotFoundOrBadUUID(err) {
			return reconciliationdb.ReconciliationItem{}, ErrItemNotFound
		}
		return reconciliationdb.ReconciliationItem{}, fmt.Errorf("get reconciliation item: %w", err)
	}
	if item.ResolutionStatus == "not_applicable" || item.MatchStatus == "matched" {
		return reconciliationdb.ReconciliationItem{}, ErrItemNotResolvable
	}

	updated, err := q.ResolveReconciliationItem(ctx, reconciliationdb.ResolveReconciliationItemParams{
		ID:               in.ItemID,
		RunID:            in.RunID,
		ResolutionStatus: status,
		ResolutionNote:   note,
		ResolvedBy:       toNullableUUID(in.UserID),
	})
	if err != nil {
		return reconciliationdb.ReconciliationItem{}, fmt.Errorf("resolve reconciliation item: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return reconciliationdb.ReconciliationItem{}, fmt.Errorf("commit resolve item: %w", err)
	}
	return updated, nil
}

// RunLedgerAudit checks double-entry balance and account balance projections under a RepeatableRead snapshot.
func (s *Service) RunLedgerAudit(ctx context.Context, userID string) (reconciliationdb.LedgerAuditRun, error) {
	if s.pool == nil {
		return reconciliationdb.LedgerAuditRun{}, errors.New("database pool is not configured")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("begin ledger audit tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	q := reconciliationdb.New(tx)
	checkedTxns, err := q.CountLedgerTransactionsByUser(ctx, userID)
	if err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("count ledger transactions: %w", err)
	}
	unbalanced, err := q.FindUnbalancedLedgerTransactionsByUser(ctx, userID)
	if err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("find unbalanced ledger transactions: %w", err)
	}
	checkedAccs, err := q.CountAccountsByUser(ctx, userID)
	if err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("count accounts: %w", err)
	}
	drifted, err := q.FindDriftedAccountsByUser(ctx, userID)
	if err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("find drifted accounts: %w", err)
	}

	anomalies := make([]LedgerAnomaly, 0, len(unbalanced)+len(drifted))
	for _, u := range unbalanced {
		anomalies = append(anomalies, LedgerAnomaly{
			Type:          "unbalanced_transaction",
			TransactionID: u.TransactionID,
			Reference:     u.Reference,
			EntryCount:    u.EntryCount,
			CurrencyCount: u.CurrencyCount,
			TotalDebit:    u.TotalDebit,
			TotalCredit:   u.TotalCredit,
		})
	}
	for _, d := range drifted {
		anomalies = append(anomalies, LedgerAnomaly{
			Type:            "balance_drift",
			AccountID:       d.AccountID,
			AccountNumber:   d.AccountNumber,
			Currency:        d.Currency,
			CachedBalance:   d.CachedBalance,
			ComputedBalance: d.ComputedBalance,
		})
	}

	rawAnomalies, err := json.Marshal(anomalies)
	if err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("marshal ledger anomalies: %w", err)
	}

	auditStatus := "healthy"
	if len(anomalies) > 0 {
		auditStatus = "anomalies_found"
	}

	auditRun, err := q.CreateLedgerAuditRun(ctx, reconciliationdb.CreateLedgerAuditRunParams{
		UserID:                 userID,
		Status:                 auditStatus,
		CheckedTransactions:    int32(checkedTxns),
		UnbalancedTransactions: int32(len(unbalanced)),
		CheckedAccounts:        int32(checkedAccs),
		DriftedAccounts:        int32(len(drifted)),
		Anomalies:              rawAnomalies,
	})
	if err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("create ledger audit run: %w", err)
	}

	if s.outbox != nil {
		if err := s.outbox.PublishTx(ctx, tx, webhook.Event{
			UserID:        userID,
			AggregateType: "ledger_audit_run",
			AggregateID:   auditRun.ID,
			EventType:     "reconciliation.ledger_audit_completed",
			Payload: map[string]any{
				"audit_id":                auditRun.ID,
				"status":                  auditRun.Status,
				"checked_transactions":    auditRun.CheckedTransactions,
				"unbalanced_transactions": auditRun.UnbalancedTransactions,
				"checked_accounts":        auditRun.CheckedAccounts,
				"drifted_accounts":        auditRun.DriftedAccounts,
			},
		}); err != nil {
			return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("publish ledger audit outbox event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return reconciliationdb.LedgerAuditRun{}, fmt.Errorf("commit ledger audit: %w", err)
	}
	return auditRun, nil
}

// ListLedgerAudits returns paginated ledger audit runs initiated by userID.
func (s *Service) ListLedgerAudits(ctx context.Context, userID string, limit, offset int32) ([]reconciliationdb.LedgerAuditRun, error) {
	if s.pool == nil {
		return nil, errors.New("database pool is not configured")
	}
	limit, offset = normalizePagination(limit, offset)
	runs, err := reconciliationdb.New(s.pool).ListLedgerAuditRunsByUser(ctx, reconciliationdb.ListLedgerAuditRunsByUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list ledger audit runs: %w", err)
	}
	if runs == nil {
		return []reconciliationdb.LedgerAuditRun{}, nil
	}
	return runs, nil
}

func normalizePagination(limit, offset int32) (int32, int32) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func toNullableUUID(id string) pgtype.UUID {
	var u pgtype.UUID
	if strings.TrimSpace(id) == "" {
		return u
	}
	_ = u.Scan(id)
	return u
}

func toNullableInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func toNullableText(s string) pgtype.Text {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: trimmed, Valid: true}
}

func isNotFoundOrBadUUID(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
