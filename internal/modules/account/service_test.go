package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/jackc/pgx/v5"
)

type mockAccountQuerier struct {
	accounts map[string]accountdb.Account
	seq      int
}

func newMockQuerier() *mockAccountQuerier {
	return &mockAccountQuerier{accounts: make(map[string]accountdb.Account)}
}

func (m *mockAccountQuerier) CreateAccount(_ context.Context, arg accountdb.CreateAccountParams) (accountdb.Account, error) {
	m.seq++
	id := "00000000-0000-0000-0000-00000000000" + string(rune('0'+m.seq))
	acc := accountdb.Account{
		ID:            id,
		UserID:        arg.UserID,
		AccountNumber: arg.AccountNumber,
		Currency:      arg.Currency,
		Status:        "active",
		BalanceAmount: 0,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	m.accounts[id] = acc
	return acc, nil
}

func (m *mockAccountQuerier) GetAccountByID(_ context.Context, id string) (accountdb.Account, error) {
	acc, ok := m.accounts[id]
	if !ok {
		return accountdb.Account{}, pgx.ErrNoRows
	}
	return acc, nil
}

func (m *mockAccountQuerier) GetAccountsForUpdate(_ context.Context, ids []string) ([]accountdb.Account, error) {
	var out []accountdb.Account
	for _, id := range ids {
		if acc, ok := m.accounts[id]; ok {
			out = append(out, acc)
		}
	}
	return out, nil
}

func (m *mockAccountQuerier) ListAccountsByUserID(_ context.Context, userID string) ([]accountdb.Account, error) {
	var out []accountdb.Account
	for _, acc := range m.accounts {
		if acc.UserID == userID {
			out = append(out, acc)
		}
	}
	return out, nil
}

func (m *mockAccountQuerier) UpdateAccountBalance(_ context.Context, arg accountdb.UpdateAccountBalanceParams) (accountdb.Account, error) {
	acc, ok := m.accounts[arg.ID]
	if !ok {
		return accountdb.Account{}, pgx.ErrNoRows
	}
	acc.BalanceAmount += arg.BalanceAmount
	m.accounts[arg.ID] = acc
	return acc, nil
}

func (m *mockAccountQuerier) UpdateAccountStatus(_ context.Context, arg accountdb.UpdateAccountStatusParams) (accountdb.Account, error) {
	acc, ok := m.accounts[arg.ID]
	if !ok {
		return accountdb.Account{}, pgx.ErrNoRows
	}
	acc.Status = arg.Status
	m.accounts[arg.ID] = acc
	return acc, nil
}

func TestService_CreateAndOwnershipAndFreeze(t *testing.T) {
	q := newMockQuerier()
	svc := account.NewService(q)
	ctx := context.Background()

	if _, err := svc.CreateAccount(ctx, "user-1", "EUR"); !errors.Is(err, account.ErrUnsupportedCurrency) {
		t.Fatalf("expected ErrUnsupportedCurrency, got %v", err)
	}

	acc, err := svc.CreateAccount(ctx, "user-1", "vnd")
	if err != nil {
		t.Fatalf("create account failed: %v", err)
	}
	if acc.Currency != "VND" || acc.Status != "active" || acc.BalanceAmount != 0 {
		t.Fatalf("unexpected account: %+v", acc)
	}

	if _, err := svc.GetAccount(ctx, "user-2", acc.ID); !errors.Is(err, account.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-owner, got %v", err)
	}

	frozen, err := svc.FreezeAccount(ctx, "user-1", acc.ID)
	if err != nil || frozen.Status != "frozen" {
		t.Fatalf("expected frozen account, got %+v err=%v", frozen, err)
	}

	unfrozen, err := svc.UnfreezeAccount(ctx, "user-1", acc.ID)
	if err != nil || unfrozen.Status != "active" {
		t.Fatalf("expected active account, got %+v err=%v", unfrozen, err)
	}
}
