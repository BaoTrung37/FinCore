package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	paymentdb "github.com/BaoTrung37/FinCore/internal/modules/payment/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrMissingIdempotencyKey = errors.New("Idempotency-Key header is required")
	ErrInvalidAmount         = errors.New("payment amount must be positive")
	ErrUnsupportedCurrency   = errors.New("unsupported currency")
	ErrInvalidBankDetails    = errors.New("bank_code and bank_account_number are required")
	ErrInvalidCallbackStatus = errors.New("callback status must be completed or failed")
	ErrIdempotencyConflict   = errors.New("idempotency key already used with different payload")
	ErrPaymentNotFound       = errors.New("payment not found")
	ErrForbidden             = errors.New("forbidden payment access")
)

// CreatePaymentInput defines the parameters for initiating a deposit or withdrawal.
type CreatePaymentInput struct {
	UserID            string
	AccountID         string
	IdempotencyKey    string
	Amount            int64
	Currency          string
	BankCode          string
	BankAccountNumber string
}

// CreatePaymentResult returns the persisted payment and whether it was replayed from idempotency cache.
type CreatePaymentResult struct {
	Payment  paymentdb.Payment
	Replayed bool
}

// BankCallbackInput defines an asynchronous settlement notification from the bank gateway.
type BankCallbackInput struct {
	ExternalRef   string
	Status        string // "completed" or "failed"
	FailureReason string
}

// Service orchestrates external deposits, withdrawals, and bank settlement callbacks.
type Service struct {
	pool       *pgxpool.Pool
	accountSvc *account.Service
	ledgerSvc  *ledger.Service
	gateway    BankGateway
}

// NewService constructs a payment Service.
func NewService(pool *pgxpool.Pool, accountSvc *account.Service, ledgerSvc *ledger.Service, gateway BankGateway) *Service {
	if gateway == nil {
		gateway = NewMockBankGateway()
	}
	return &Service{
		pool:       pool,
		accountSvc: accountSvc,
		ledgerSvc:  ledgerSvc,
		gateway:    gateway,
	}
}

// ComputeRequestHash computes a canonical SHA-256 hex digest for idempotency payload comparison.
func ComputeRequestHash(direction, accountID string, amount int64, currency, bankCode, bankAccountNumber string) string {
	canonical := fmt.Sprintf("%s|%s|%d|%s|%s|%s",
		strings.ToLower(strings.TrimSpace(direction)),
		strings.TrimSpace(accountID),
		amount,
		strings.ToUpper(strings.TrimSpace(currency)),
		strings.ToUpper(strings.TrimSpace(bankCode)),
		strings.TrimSpace(bankAccountNumber),
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// InitiateDeposit initiates an external deposit into a user's FinCore account.
func (s *Service) InitiateDeposit(ctx context.Context, in CreatePaymentInput) (CreatePaymentResult, error) {
	return s.initiate(ctx, "deposit", in)
}

// InitiateWithdrawal initiates an external withdrawal from a user's FinCore account.
func (s *Service) InitiateWithdrawal(ctx context.Context, in CreatePaymentInput) (CreatePaymentResult, error) {
	return s.initiate(ctx, "withdrawal", in)
}

func (s *Service) initiate(ctx context.Context, direction string, in CreatePaymentInput) (CreatePaymentResult, error) {
	idemKey := strings.TrimSpace(in.IdempotencyKey)
	if idemKey == "" {
		return CreatePaymentResult{}, ErrMissingIdempotencyKey
	}
	if in.Amount <= 0 {
		return CreatePaymentResult{}, ErrInvalidAmount
	}
	curr := strings.ToUpper(strings.TrimSpace(in.Currency))
	if curr != "VND" && curr != "USD" {
		return CreatePaymentResult{}, ErrUnsupportedCurrency
	}
	bankCode := strings.ToUpper(strings.TrimSpace(in.BankCode))
	bankAccNum := strings.TrimSpace(in.BankAccountNumber)
	if bankCode == "" || bankAccNum == "" {
		return CreatePaymentResult{}, ErrInvalidBankDetails
	}
	if s.pool == nil || s.accountSvc == nil || s.ledgerSvc == nil || s.gateway == nil {
		return CreatePaymentResult{}, errors.New("payment service dependencies not configured")
	}

	reqHash := ComputeRequestHash(direction, in.AccountID, in.Amount, curr, bankCode, bankAccNum)
	pq := paymentdb.New(s.pool)

	// Fast-path idempotency check.
	if existing, err := pq.GetPaymentByIdempotencyKey(ctx, paymentdb.GetPaymentByIdempotencyKeyParams{
		UserID:         in.UserID,
		IdempotencyKey: idemKey,
	}); err == nil {
		if existing.RequestHash != reqHash {
			return CreatePaymentResult{}, ErrIdempotencyConflict
		}
		return CreatePaymentResult{Payment: existing, Replayed: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return CreatePaymentResult{}, fmt.Errorf("check idempotency key: %w", err)
	}

	// Enforce account ownership, currency, status, and pre-check withdrawal balance.
	acc, err := s.accountSvc.GetAccount(ctx, in.UserID, in.AccountID)
	if err != nil {
		return CreatePaymentResult{}, err
	}
	if acc.Currency != curr {
		return CreatePaymentResult{}, ledger.ErrCurrencyMismatch
	}
	switch acc.Status {
	case "frozen":
		return CreatePaymentResult{}, ledger.ErrAccountFrozen
	case "closed":
		return CreatePaymentResult{}, ledger.ErrAccountClosed
	}
	if direction == "withdrawal" && acc.BalanceAmount < in.Amount {
		return CreatePaymentResult{}, ledger.ErrInsufficientBalance
	}

	bankReq := BankRequest{
		Direction:         direction,
		AccountID:         acc.ID,
		Amount:            in.Amount,
		Currency:          curr,
		BankCode:          bankCode,
		BankAccountNumber: bankAccNum,
		IdempotencyKey:    idemKey,
	}

	var bankResp BankResponse
	if direction == "deposit" {
		bankResp, err = s.gateway.InitiateDeposit(ctx, bankReq)
	} else {
		bankResp, err = s.gateway.InitiateWithdrawal(ctx, bankReq)
	}
	if err != nil {
		return CreatePaymentResult{}, fmt.Errorf("bank gateway error: %w", err)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CreatePaymentResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var ltxUUID pgtype.UUID
	if bankResp.Status == "completed" {
		ltxID, err := s.postPaymentLedgerInTx(ctx, tx, direction, acc.ID, in.Amount, curr, fmt.Sprintf("PAY-%s-%s", in.UserID, idemKey))
		if err != nil {
			return CreatePaymentResult{}, err
		}
		if err := ltxUUID.Scan(ltxID); err != nil {
			return CreatePaymentResult{}, fmt.Errorf("scan ledger transaction id: %w", err)
		}
	}

	var extRef pgtype.Text
	if strings.TrimSpace(bankResp.ExternalRef) != "" {
		extRef = pgtype.Text{String: bankResp.ExternalRef, Valid: true}
	}

	p, err := paymentdb.New(tx).CreatePayment(ctx, paymentdb.CreatePaymentParams{
		UserID:              in.UserID,
		AccountID:           acc.ID,
		IdempotencyKey:      idemKey,
		RequestHash:         reqHash,
		Direction:           direction,
		Amount:              in.Amount,
		Currency:            curr,
		BankCode:            bankCode,
		BankAccountNumber:   bankAccNum,
		ExternalRef:         extRef,
		Status:              bankResp.Status,
		FailureReason:       bankResp.FailureReason,
		LedgerTransactionID: ltxUUID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(ctx)
			existing, lookupErr := pq.GetPaymentByIdempotencyKey(ctx, paymentdb.GetPaymentByIdempotencyKeyParams{
				UserID:         in.UserID,
				IdempotencyKey: idemKey,
			})
			if lookupErr == nil {
				if existing.RequestHash != reqHash {
					return CreatePaymentResult{}, ErrIdempotencyConflict
				}
				return CreatePaymentResult{Payment: existing, Replayed: true}, nil
			}
		}
		return CreatePaymentResult{}, fmt.Errorf("create payment record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CreatePaymentResult{}, fmt.Errorf("commit payment tx: %w", err)
	}
	return CreatePaymentResult{Payment: p, Replayed: false}, nil
}

// HandleBankCallback settles a pending payment idempotently when notified by the bank gateway.
func (s *Service) HandleBankCallback(ctx context.Context, in BankCallbackInput) (paymentdb.Payment, error) {
	extRef := strings.TrimSpace(in.ExternalRef)
	status := strings.ToLower(strings.TrimSpace(in.Status))
	if extRef == "" {
		return paymentdb.Payment{}, ErrPaymentNotFound
	}
	if status != "completed" && status != "failed" {
		return paymentdb.Payment{}, ErrInvalidCallbackStatus
	}
	if s.pool == nil || s.accountSvc == nil || s.ledgerSvc == nil {
		return paymentdb.Payment{}, errors.New("payment service dependencies not configured")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return paymentdb.Payment{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	pq := paymentdb.New(tx)
	p, err := pq.GetPaymentByExternalRefForUpdate(ctx, extRef)
	if err != nil {
		if isNotFoundOrBadUUID(err) {
			return paymentdb.Payment{}, ErrPaymentNotFound
		}
		return paymentdb.Payment{}, fmt.Errorf("lock payment: %w", err)
	}

	// Idempotent settlement: already finalized.
	if p.Status != "pending" {
		return p, nil
	}

	var ltxUUID pgtype.UUID
	failureReason := strings.TrimSpace(in.FailureReason)

	if status == "completed" {
		ltxID, err := s.postPaymentLedgerInTx(ctx, tx, p.Direction, p.AccountID, p.Amount, p.Currency, fmt.Sprintf("PAY-%s-%s", p.UserID, p.IdempotencyKey))
		if err != nil {
			return paymentdb.Payment{}, err
		}
		if err := ltxUUID.Scan(ltxID); err != nil {
			return paymentdb.Payment{}, fmt.Errorf("scan ledger transaction id: %w", err)
		}
		failureReason = ""
	}

	updated, err := pq.UpdatePaymentStatus(ctx, paymentdb.UpdatePaymentStatusParams{
		ID:                  p.ID,
		Status:              status,
		FailureReason:       failureReason,
		LedgerTransactionID: ltxUUID,
	})
	if err != nil {
		return paymentdb.Payment{}, fmt.Errorf("update payment status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return paymentdb.Payment{}, fmt.Errorf("commit callback tx: %w", err)
	}
	return updated, nil
}

// GetPayment returns a payment if owned by the authenticated user.
func (s *Service) GetPayment(ctx context.Context, userID, paymentID string) (paymentdb.Payment, error) {
	if s.pool == nil {
		return paymentdb.Payment{}, errors.New("payment service not configured")
	}
	p, err := paymentdb.New(s.pool).GetPaymentByID(ctx, paymentID)
	if err != nil {
		if isNotFoundOrBadUUID(err) {
			return paymentdb.Payment{}, ErrPaymentNotFound
		}
		return paymentdb.Payment{}, fmt.Errorf("get payment: %w", err)
	}
	if p.UserID != userID {
		return paymentdb.Payment{}, ErrForbidden
	}
	return p, nil
}

// ListPayments returns paginated payments owned by the authenticated user.
func (s *Service) ListPayments(ctx context.Context, userID string, limit, offset int32) ([]paymentdb.Payment, error) {
	if s.pool == nil {
		return nil, errors.New("payment service not configured")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return paymentdb.New(s.pool).ListPaymentsByUserID(ctx, paymentdb.ListPaymentsByUserIDParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
}

func (s *Service) postPaymentLedgerInTx(
	ctx context.Context,
	tx pgx.Tx,
	direction, accountID string,
	amount int64,
	currency, reference string,
) (string, error) {
	settlement, err := s.accountSvc.GetSettlementAccountByCurrency(ctx, currency)
	if err != nil {
		return "", err
	}

	var entries []ledger.EntryInput
	if direction == "deposit" {
		entries = []ledger.EntryInput{
			{AccountID: settlement.ID, EntryType: "debit", Amount: amount, Currency: currency},
			{AccountID: accountID, EntryType: "credit", Amount: amount, Currency: currency},
		}
	} else {
		entries = []ledger.EntryInput{
			{AccountID: accountID, EntryType: "debit", Amount: amount, Currency: currency},
			{AccountID: settlement.ID, EntryType: "credit", Amount: amount, Currency: currency},
		}
	}

	ltx, _, err := s.ledgerSvc.PostInTx(ctx, tx, ledger.PostTransactionInput{
		Reference:       reference,
		TransactionType: direction,
		Entries:         entries,
	})
	if err != nil {
		return "", err
	}
	return ltx.ID, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isNotFoundOrBadUUID(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
