package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	transferdb "github.com/BaoTrung37/FinCore/internal/modules/transfer/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrMissingIdempotencyKey = errors.New("Idempotency-Key header is required")
	ErrSelfTransfer          = errors.New("source and destination accounts must differ")
	ErrInvalidAmount         = errors.New("transfer amount must be positive")
	ErrUnsupportedCurrency   = errors.New("unsupported currency")
	ErrIdempotencyConflict   = errors.New("idempotency key already used with different payload")
	ErrTransferNotFound      = errors.New("transfer not found")
	ErrForbidden             = errors.New("forbidden transfer access")
)

// CreateTransferInput defines the parameters for an internal account transfer.
type CreateTransferInput struct {
	UserID               string
	IdempotencyKey       string
	SourceAccountID      string
	DestinationAccountID string
	Amount               int64
	Currency             string
}

// CreateTransferResult returns the persisted transfer and whether it was replayed from idempotency cache.
type CreateTransferResult struct {
	Transfer transferdb.Transfer
	Replayed bool
}

// Service orchestrates atomic, idempotent internal transfers.
type Service struct {
	pool       *pgxpool.Pool
	accountSvc *account.Service
	ledgerSvc  *ledger.Service
}

// NewService constructs a transfer Service.
func NewService(pool *pgxpool.Pool, accountSvc *account.Service, ledgerSvc *ledger.Service) *Service {
	return &Service{
		pool:       pool,
		accountSvc: accountSvc,
		ledgerSvc:  ledgerSvc,
	}
}

// ComputeRequestHash computes a canonical SHA-256 hex digest for idempotency payload comparison.
func ComputeRequestHash(srcID, dstID string, amount int64, currency string) string {
	canonical := fmt.Sprintf("%s|%s|%d|%s",
		strings.TrimSpace(srcID),
		strings.TrimSpace(dstID),
		amount,
		strings.ToUpper(strings.TrimSpace(currency)),
	)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// CreateTransfer executes an atomic internal transfer with idempotency and deadlock-free locking.
func (s *Service) CreateTransfer(ctx context.Context, in CreateTransferInput) (CreateTransferResult, error) {
	idemKey := strings.TrimSpace(in.IdempotencyKey)
	if idemKey == "" {
		return CreateTransferResult{}, ErrMissingIdempotencyKey
	}
	if in.SourceAccountID == "" || in.DestinationAccountID == "" || in.SourceAccountID == in.DestinationAccountID {
		return CreateTransferResult{}, ErrSelfTransfer
	}
	if in.Amount <= 0 {
		return CreateTransferResult{}, ErrInvalidAmount
	}
	curr := strings.ToUpper(strings.TrimSpace(in.Currency))
	if curr != "VND" && curr != "USD" {
		return CreateTransferResult{}, ErrUnsupportedCurrency
	}
	if s.pool == nil || s.accountSvc == nil || s.ledgerSvc == nil {
		return CreateTransferResult{}, errors.New("transfer service dependencies not configured")
	}

	reqHash := ComputeRequestHash(in.SourceAccountID, in.DestinationAccountID, in.Amount, curr)
	tq := transferdb.New(s.pool)

	// Fast-path idempotency check before opening a write transaction.
	if existing, err := tq.GetTransferByIdempotencyKey(ctx, transferdb.GetTransferByIdempotencyKeyParams{
		UserID:         in.UserID,
		IdempotencyKey: idemKey,
	}); err == nil {
		if existing.RequestHash != reqHash {
			return CreateTransferResult{}, ErrIdempotencyConflict
		}
		return CreateTransferResult{Transfer: existing, Replayed: true}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return CreateTransferResult{}, fmt.Errorf("check idempotency key: %w", err)
	}

	// Enforce ownership of the source account.
	if _, err := s.accountSvc.GetAccount(ctx, in.UserID, in.SourceAccountID); err != nil {
		return CreateTransferResult{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CreateTransferResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	ltx, _, err := s.ledgerSvc.PostInTx(ctx, tx, ledger.PostTransactionInput{
		Reference:       fmt.Sprintf("TRF-%s-%s", in.UserID, idemKey),
		TransactionType: "transfer",
		Entries: []ledger.EntryInput{
			{AccountID: in.SourceAccountID, EntryType: "debit", Amount: in.Amount, Currency: curr},
			{AccountID: in.DestinationAccountID, EntryType: "credit", Amount: in.Amount, Currency: curr},
		},
	})
	if err != nil {
		return CreateTransferResult{}, err
	}

	tr, err := transferdb.New(tx).CreateTransfer(ctx, transferdb.CreateTransferParams{
		UserID:               in.UserID,
		IdempotencyKey:       idemKey,
		RequestHash:          reqHash,
		SourceAccountID:      in.SourceAccountID,
		DestinationAccountID: in.DestinationAccountID,
		Amount:               in.Amount,
		Currency:             curr,
		Status:               "completed",
		LedgerTransactionID:  ltx.ID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(ctx)
			existing, lookupErr := tq.GetTransferByIdempotencyKey(ctx, transferdb.GetTransferByIdempotencyKeyParams{
				UserID:         in.UserID,
				IdempotencyKey: idemKey,
			})
			if lookupErr == nil {
				if existing.RequestHash != reqHash {
					return CreateTransferResult{}, ErrIdempotencyConflict
				}
				return CreateTransferResult{Transfer: existing, Replayed: true}, nil
			}
		}
		return CreateTransferResult{}, fmt.Errorf("create transfer record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateTransferResult{}, fmt.Errorf("commit transfer tx: %w", err)
	}
	return CreateTransferResult{Transfer: tr, Replayed: false}, nil
}

// GetTransfer returns a transfer if the authenticated user initiated it.
func (s *Service) GetTransfer(ctx context.Context, userID, transferID string) (transferdb.Transfer, error) {
	if s.pool == nil {
		return transferdb.Transfer{}, errors.New("transfer service not configured")
	}
	tr, err := transferdb.New(s.pool).GetTransferByID(ctx, transferID)
	if err != nil {
		if isNotFoundOrBadUUID(err) {
			return transferdb.Transfer{}, ErrTransferNotFound
		}
		return transferdb.Transfer{}, fmt.Errorf("get transfer: %w", err)
	}
	if tr.UserID != userID {
		return transferdb.Transfer{}, ErrForbidden
	}
	return tr, nil
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
