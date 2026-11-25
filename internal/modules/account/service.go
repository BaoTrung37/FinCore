package account

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrAccountNotFound     = errors.New("account not found")
	ErrForbidden           = errors.New("forbidden account access")
	ErrAccountClosed       = errors.New("account is closed")
)

// BalanceResponse represents an account's current balance projection.
type BalanceResponse struct {
	AccountID     string `json:"account_id"`
	Currency      string `json:"currency"`
	BalanceAmount int64  `json:"balance_amount"`
	Status        string `json:"status"`
}

// Service implements account management business logic.
type Service struct {
	q accountdb.Querier
}

// NewService constructs an account Service.
func NewService(q accountdb.Querier) *Service {
	return &Service{q: q}
}

// CreateAccount opens a new active account for userID in the specified currency (VND or USD).
func (s *Service) CreateAccount(ctx context.Context, userID, currency string) (accountdb.Account, error) {
	curr := strings.ToUpper(strings.TrimSpace(currency))
	if curr != "VND" && curr != "USD" {
		return accountdb.Account{}, ErrUnsupportedCurrency
	}

	accNum, err := generateAccountNumber()
	if err != nil {
		return accountdb.Account{}, fmt.Errorf("generate account number: %w", err)
	}

	acc, err := s.q.CreateAccount(ctx, accountdb.CreateAccountParams{
		UserID:        userID,
		AccountNumber: accNum,
		Currency:      curr,
	})
	if err != nil {
		return accountdb.Account{}, fmt.Errorf("create account: %w", err)
	}
	return acc, nil
}

// ListAccounts returns all accounts owned by userID.
func (s *Service) ListAccounts(ctx context.Context, userID string) ([]accountdb.Account, error) {
	accs, err := s.q.ListAccountsByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return accs, nil
}

// GetAccount returns an account if owned by userID.
func (s *Service) GetAccount(ctx context.Context, userID, accountID string) (accountdb.Account, error) {
	acc, err := s.q.GetAccountByID(ctx, accountID)
	if err != nil {
		if isNotFoundOrBadUUID(err) {
			return accountdb.Account{}, ErrAccountNotFound
		}
		return accountdb.Account{}, fmt.Errorf("get account: %w", err)
	}
	if acc.UserID != userID {
		return accountdb.Account{}, ErrForbidden
	}
	return acc, nil
}

// FreezeAccount marks an owned, non-closed account as frozen.
func (s *Service) FreezeAccount(ctx context.Context, userID, accountID string) (accountdb.Account, error) {
	return s.setStatus(ctx, userID, accountID, "frozen")
}

// UnfreezeAccount marks an owned, non-closed account as active.
func (s *Service) UnfreezeAccount(ctx context.Context, userID, accountID string) (accountdb.Account, error) {
	return s.setStatus(ctx, userID, accountID, "active")
}

// GetBalance returns the balance projection of an owned account.
func (s *Service) GetBalance(ctx context.Context, userID, accountID string) (BalanceResponse, error) {
	acc, err := s.GetAccount(ctx, userID, accountID)
	if err != nil {
		return BalanceResponse{}, err
	}
	return BalanceResponse{
		AccountID:     acc.ID,
		Currency:      acc.Currency,
		BalanceAmount: acc.BalanceAmount,
		Status:        acc.Status,
	}, nil
}

func (s *Service) setStatus(ctx context.Context, userID, accountID, status string) (accountdb.Account, error) {
	acc, err := s.GetAccount(ctx, userID, accountID)
	if err != nil {
		return accountdb.Account{}, err
	}
	if acc.Status == "closed" {
		return accountdb.Account{}, ErrAccountClosed
	}
	updated, err := s.q.UpdateAccountStatus(ctx, accountdb.UpdateAccountStatusParams{
		ID:     acc.ID,
		Status: status,
	})
	if err != nil {
		return accountdb.Account{}, fmt.Errorf("update account status: %w", err)
	}
	return updated, nil
}

func isNotFoundOrBadUUID(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

func generateAccountNumber() (string, error) {
	max := big.NewInt(1_000_000_000_000) // 12 digits
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("FC%012d", n.Int64()), nil
}
