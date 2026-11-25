package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestAccountAndLedger_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://fincore_app:local_dev_password@localhost:5432/fincore?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := database.NewPostgresPool(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unreachable; skipping integration test: %v", err)
	}
	defer pool.Close()

	if err := database.MigrateUp(dsn, migrations.FS); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tm := auth.NewTokenManager("integration-test-secret-32-bytes!!", time.Hour)
	authSvc := auth.NewService(auth.NewPostgresRepository(pool), tm)
	accSvc := account.NewService(accountdb.New(pool))
	accHandler := account.NewHandler(accSvc, log)
	ledgerSvc := ledger.NewService(pool)

	router := httpserver.NewRouter(log, pool)
	accHandler.RegisterRoutes(router, tm)

	emailA := fmt.Sprintf("m2-a-%d@example.com", time.Now().UnixNano())
	emailB := fmt.Sprintf("m2-b-%d@example.com", time.Now().UnixNano())
	userA, err := authSvc.Register(context.Background(), emailA, "password123")
	if err != nil {
		t.Fatalf("register user A: %v", err)
	}
	userB, err := authSvc.Register(context.Background(), emailB, "password123")
	if err != nil {
		t.Fatalf("register user B: %v", err)
	}

	acc1, err := accSvc.CreateAccount(context.Background(), userA.User.ID, "VND")
	if err != nil {
		t.Fatalf("create acc1: %v", err)
	}
	acc2, err := accSvc.CreateAccount(context.Background(), userB.User.ID, "VND")
	if err != nil {
		t.Fatalf("create acc2: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM ledger_transactions WHERE reference LIKE 'M2-INT-%'")
		_, _ = pool.Exec(context.Background(), "DELETE FROM accounts WHERE id IN ($1, $2)", acc1.ID, acc2.ID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id IN ($1, $2)", userA.User.ID, userB.User.ID)
	})

	// Seed acc1 with initial balance via SQL for test setup, then test ledger transfer.
	if _, err := pool.Exec(context.Background(), "UPDATE accounts SET balance_amount = 500000 WHERE id = $1", acc1.ID); err != nil {
		t.Fatalf("seed balance: %v", err)
	}

	// 1. Cross-user HTTP access -> 403
	reqOther := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+acc1.ID, nil)
	reqOther.Header.Set("Authorization", "Bearer "+userB.AccessToken)
	recOther := httptest.NewRecorder()
	router.ServeHTTP(recOther, reqOther)
	if recOther.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on cross-user access, got %d", recOther.Code)
	}

	// 2. Freeze acc2 -> ledger rejects posting to frozen account
	if _, err := accSvc.FreezeAccount(context.Background(), userB.User.ID, acc2.ID); err != nil {
		t.Fatalf("freeze acc2: %v", err)
	}
	_, _, err = ledgerSvc.PostTransaction(context.Background(), ledger.PostTransactionInput{
		Reference:       fmt.Sprintf("M2-INT-FRZ-%d", time.Now().UnixNano()),
		TransactionType: "transfer",
		Entries: []ledger.EntryInput{
			{AccountID: acc1.ID, EntryType: "debit", Amount: 100000, Currency: "VND"},
			{AccountID: acc2.ID, EntryType: "credit", Amount: 100000, Currency: "VND"},
		},
	})
	if !errors.Is(err, ledger.ErrAccountFrozen) {
		t.Fatalf("expected ErrAccountFrozen, got %v", err)
	}
	if _, err := accSvc.UnfreezeAccount(context.Background(), userB.User.ID, acc2.ID); err != nil {
		t.Fatalf("unfreeze acc2: %v", err)
	}

	// 3. Balanced double-entry transfer
	txRef := fmt.Sprintf("M2-INT-TX-%d", time.Now().UnixNano())
	ltx, entries, err := ledgerSvc.PostTransaction(context.Background(), ledger.PostTransactionInput{
		Reference:       txRef,
		TransactionType: "transfer",
		Entries: []ledger.EntryInput{
			{AccountID: acc1.ID, EntryType: "debit", Amount: 100000, Currency: "VND"},
			{AccountID: acc2.ID, EntryType: "credit", Amount: 100000, Currency: "VND"},
		},
	})
	if err != nil || len(entries) != 2 {
		t.Fatalf("post transaction failed: err=%v entries=%d", err, len(entries))
	}

	bal1, _ := accSvc.GetBalance(context.Background(), userA.User.ID, acc1.ID)
	bal2, _ := accSvc.GetBalance(context.Background(), userB.User.ID, acc2.ID)
	if bal1.BalanceAmount != 400000 || bal2.BalanceAmount != 100000 {
		t.Fatalf("unexpected balances after transfer: acc1=%d acc2=%d", bal1.BalanceAmount, bal2.BalanceAmount)
	}

	// 4. Append-only trigger blocks UPDATE and DELETE on ledger_entries
	if _, err := pool.Exec(context.Background(), "UPDATE ledger_entries SET amount = 999 WHERE id = $1", entries[0].ID); err == nil {
		t.Fatal("expected UPDATE on ledger_entries to be blocked by trigger")
	}
	if _, err := pool.Exec(context.Background(), "DELETE FROM ledger_entries WHERE id = $1", entries[0].ID); err == nil {
		t.Fatal("expected DELETE on ledger_entries to be blocked by trigger")
	}

	// 5. Reversal restores balances and blocks double-reversal
	revRef := fmt.Sprintf("M2-INT-REV-%d", time.Now().UnixNano())
	if _, _, err := ledgerSvc.ReverseTransaction(context.Background(), ltx.ID, revRef); err != nil {
		t.Fatalf("reverse transaction: %v", err)
	}
	bal1After, _ := accSvc.GetBalance(context.Background(), userA.User.ID, acc1.ID)
	bal2After, _ := accSvc.GetBalance(context.Background(), userB.User.ID, acc2.ID)
	if bal1After.BalanceAmount != 500000 || bal2After.BalanceAmount != 0 {
		t.Fatalf("unexpected balances after reversal: acc1=%d acc2=%d", bal1After.BalanceAmount, bal2After.BalanceAmount)
	}
	if _, _, err := ledgerSvc.ReverseTransaction(context.Background(), ltx.ID, revRef+"-2"); !errors.Is(err, ledger.ErrAlreadyReversed) {
		t.Fatalf("expected ErrAlreadyReversed on second reversal, got %v", err)
	}
}
