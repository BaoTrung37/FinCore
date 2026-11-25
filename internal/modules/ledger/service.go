package ledger

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	ledgerdb "github.com/BaoTrung37/FinCore/internal/modules/ledger/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidReference      = errors.New("transaction reference is required")
	ErrInvalidEntries        = errors.New("at least two ledger entries are required")
	ErrInvalidEntryType      = errors.New("entry type must be debit or credit")
	ErrInvalidAmount         = errors.New("entry amount must be positive")
	ErrCurrencyMismatch      = errors.New("mixed currencies are not allowed")
	ErrUnbalancedTransaction = errors.New("total debit must equal total credit")
	ErrDuplicateReference    = errors.New("duplicate transaction reference")
	ErrAccountNotFound       = errors.New("account not found")
	ErrAccountFrozen         = errors.New("account is frozen")
	ErrAccountClosed         = errors.New("account is closed")
	ErrInsufficientBalance   = errors.New("insufficient account balance")
	ErrTransactionNotFound   = errors.New("ledger transaction not found")
	ErrAlreadyReversed       = errors.New("ledger transaction is already reversed")
)

// EntryInput defines one debit or credit line in a double-entry transaction.
type EntryInput struct {
	AccountID string
	EntryType string // "debit" or "credit"
	Amount    int64  // minor units (> 0)
	Currency  string // "VND" or "USD"
}

// PostTransactionInput defines a balanced double-entry transaction request.
type PostTransactionInput struct {
	Reference       string
	TransactionType string // "deposit", "withdrawal", "transfer", "reversal", "adjustment"
	Entries         []EntryInput
}

// Service implements double-entry ledger posting and reversal inside PostgreSQL transactions.
type Service struct {
	pool *pgxpool.Pool
}

// NewService constructs a ledger Service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// SortedUniqueAccountIDs returns deduplicated account IDs in ascending order for deadlock-free locking.
func SortedUniqueAccountIDs(entries []EntryInput) []string {
	seen := make(map[string]struct{}, len(entries))
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, ok := seen[e.AccountID]; ok {
			continue
		}
		seen[e.AccountID] = struct{}{}
		ids = append(ids, e.AccountID)
	}
	sort.Strings(ids)
	return ids
}

// PostTransaction atomically records a balanced ledger transaction and updates account balance projections.
func (s *Service) PostTransaction(ctx context.Context, in PostTransactionInput) (ledgerdb.LedgerTransaction, []ledgerdb.LedgerEntry, error) {
	txCurrency, err := validateInput(in)
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, err
	}
	if s.pool == nil {
		return ledgerdb.LedgerTransaction{}, nil, errors.New("database pool is not configured")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	ltx, entries, err := s.postInTx(ctx, tx, in, txCurrency, pgtype.UUID{})
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("commit tx: %w", err)
	}
	return ltx, entries, nil
}

// PostInTx records a balanced ledger transaction inside an existing pgx.Tx.
func (s *Service) PostInTx(ctx context.Context, tx pgx.Tx, in PostTransactionInput) (ledgerdb.LedgerTransaction, []ledgerdb.LedgerEntry, error) {
	txCurrency, err := validateInput(in)
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, err
	}
	return s.postInTx(ctx, tx, in, txCurrency, pgtype.UUID{})
}

// ListAccountEntries returns paginated ledger entries for an account.
func (s *Service) ListAccountEntries(ctx context.Context, accountID string, limit, offset int32) ([]ledgerdb.LedgerEntry, error) {
	if s.pool == nil {
		return nil, errors.New("database pool is not configured")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return ledgerdb.New(s.pool).ListEntriesByAccountID(ctx, ledgerdb.ListEntriesByAccountIDParams{
		AccountID: accountID,
		Limit:     limit,
		Offset:    offset,
	})
}

// ReverseTransaction creates an opposing ledger transaction and marks the original as reversed.
func (s *Service) ReverseTransaction(ctx context.Context, txID, reference string) (ledgerdb.LedgerTransaction, []ledgerdb.LedgerEntry, error) {
	if strings.TrimSpace(reference) == "" {
		return ledgerdb.LedgerTransaction{}, nil, ErrInvalidReference
	}
	if s.pool == nil {
		return ledgerdb.LedgerTransaction{}, nil, errors.New("database pool is not configured")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	lq := ledgerdb.New(tx)
	origTx, err := lq.GetLedgerTransactionByID(ctx, txID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledgerdb.LedgerTransaction{}, nil, ErrTransactionNotFound
		}
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("get transaction: %w", err)
	}
	if origTx.Status == "reversed" {
		return ledgerdb.LedgerTransaction{}, nil, ErrAlreadyReversed
	}

	affected, err := lq.MarkLedgerTransactionReversed(ctx, origTx.ID)
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("mark reversed: %w", err)
	}
	if affected == 0 {
		return ledgerdb.LedgerTransaction{}, nil, ErrAlreadyReversed
	}

	origEntries, err := lq.ListEntriesByTransactionID(ctx, origTx.ID)
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("list original entries: %w", err)
	}
	if len(origEntries) < 2 {
		return ledgerdb.LedgerTransaction{}, nil, ErrInvalidEntries
	}

	revEntries := make([]EntryInput, len(origEntries))
	for i, e := range origEntries {
		flipped := "credit"
		if e.EntryType == "credit" {
			flipped = "debit"
		}
		revEntries[i] = EntryInput{
			AccountID: e.AccountID,
			EntryType: flipped,
			Amount:    e.Amount,
			Currency:  e.Currency,
		}
	}

	var revOf pgtype.UUID
	if err := revOf.Scan(origTx.ID); err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("scan reversal_of_id: %w", err)
	}

	revIn := PostTransactionInput{
		Reference:       reference,
		TransactionType: "reversal",
		Entries:         revEntries,
	}
	ltx, entries, err := s.postInTx(ctx, tx, revIn, origEntries[0].Currency, revOf)
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("commit reversal tx: %w", err)
	}
	return ltx, entries, nil
}

func (s *Service) postInTx(
	ctx context.Context,
	tx pgx.Tx,
	in PostTransactionInput,
	txCurrency string,
	reversalOfID pgtype.UUID,
) (ledgerdb.LedgerTransaction, []ledgerdb.LedgerEntry, error) {
	aq := accountdb.New(tx)
	lq := ledgerdb.New(tx)

	sortedIDs := SortedUniqueAccountIDs(in.Entries)
	locked, err := aq.GetAccountsForUpdate(ctx, sortedIDs)
	if err != nil {
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("lock accounts: %w", err)
	}
	if len(locked) != len(sortedIDs) {
		return ledgerdb.LedgerTransaction{}, nil, ErrAccountNotFound
	}

	accMap := make(map[string]accountdb.Account, len(locked))
	for _, acc := range locked {
		switch acc.Status {
		case "frozen":
			return ledgerdb.LedgerTransaction{}, nil, ErrAccountFrozen
		case "closed":
			return ledgerdb.LedgerTransaction{}, nil, ErrAccountClosed
		}
		if acc.Currency != txCurrency {
			return ledgerdb.LedgerTransaction{}, nil, ErrCurrencyMismatch
		}
		accMap[acc.ID] = acc
	}

	deltas := make(map[string]int64, len(sortedIDs))
	for _, e := range in.Entries {
		if e.EntryType == "credit" {
			deltas[e.AccountID] += e.Amount
		} else {
			deltas[e.AccountID] -= e.Amount
		}
	}

	for _, id := range sortedIDs {
		if accMap[id].BalanceAmount+deltas[id] < 0 {
			return ledgerdb.LedgerTransaction{}, nil, ErrInsufficientBalance
		}
	}

	ltx, err := lq.CreateLedgerTransaction(ctx, ledgerdb.CreateLedgerTransactionParams{
		Reference:       in.Reference,
		TransactionType: in.TransactionType,
		ReversalOfID:    reversalOfID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return ledgerdb.LedgerTransaction{}, nil, ErrDuplicateReference
		}
		return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("create ledger transaction: %w", err)
	}

	entries := make([]ledgerdb.LedgerEntry, 0, len(in.Entries))
	for _, e := range in.Entries {
		entry, err := lq.CreateLedgerEntry(ctx, ledgerdb.CreateLedgerEntryParams{
			TransactionID: ltx.ID,
			AccountID:     e.AccountID,
			EntryType:     e.EntryType,
			Amount:        e.Amount,
			Currency:      txCurrency,
		})
		if err != nil {
			return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("create ledger entry: %w", err)
		}
		entries = append(entries, entry)
	}

	for _, id := range sortedIDs {
		if deltas[id] == 0 {
			continue
		}
		if _, err := aq.UpdateAccountBalance(ctx, accountdb.UpdateAccountBalanceParams{
			ID:            id,
			BalanceAmount: deltas[id],
		}); err != nil {
			return ledgerdb.LedgerTransaction{}, nil, fmt.Errorf("update account balance: %w", err)
		}
	}

	return ltx, entries, nil
}

func validateInput(in PostTransactionInput) (string, error) {
	if strings.TrimSpace(in.Reference) == "" {
		return "", ErrInvalidReference
	}
	if len(in.Entries) < 2 {
		return "", ErrInvalidEntries
	}

	var totalDebit, totalCredit int64
	var txCurrency string

	for i, e := range in.Entries {
		if e.Amount <= 0 {
			return "", ErrInvalidAmount
		}
		curr := strings.ToUpper(strings.TrimSpace(e.Currency))
		if i == 0 {
			txCurrency = curr
		} else if curr != txCurrency {
			return "", ErrCurrencyMismatch
		}

		switch e.EntryType {
		case "debit":
			totalDebit += e.Amount
		case "credit":
			totalCredit += e.Amount
		default:
			return "", ErrInvalidEntryType
		}
	}

	if totalDebit != totalCredit {
		return "", ErrUnbalancedTransaction
	}
	return txCurrency, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
