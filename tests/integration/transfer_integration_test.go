package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	"github.com/BaoTrung37/FinCore/internal/modules/transfer"
	transferdb "github.com/BaoTrung37/FinCore/internal/modules/transfer/db"
	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestTransfer_IdempotencyAnd100Concurrent_Integration(t *testing.T) {
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
	ledgerSvc := ledger.NewService(pool)
	accSvc := account.NewService(accountdb.New(pool))
	accHandler := account.NewHandler(accSvc, log, ledgerSvc)
	transferSvc := transfer.NewService(pool, accSvc, ledgerSvc)
	transferHandler := transfer.NewHandler(transferSvc, log)

	router := httpserver.NewRouter(log, pool)
	accHandler.RegisterRoutes(router, tm)
	transferHandler.RegisterRoutes(router, tm)

	emailA := fmt.Sprintf("m3-a-%d@example.com", time.Now().UnixNano())
	emailB := fmt.Sprintf("m3-b-%d@example.com", time.Now().UnixNano())
	userA, err := authSvc.Register(context.Background(), emailA, "password123")
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	userB, err := authSvc.Register(context.Background(), emailB, "password123")
	if err != nil {
		t.Fatalf("register B: %v", err)
	}

	accA, err := accSvc.CreateAccount(context.Background(), userA.User.ID, "VND")
	if err != nil {
		t.Fatalf("create accA: %v", err)
	}
	accB, err := accSvc.CreateAccount(context.Background(), userB.User.ID, "VND")
	if err != nil {
		t.Fatalf("create accB: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM transfers WHERE user_id IN ($1, $2)", userA.User.ID, userB.User.ID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM ledger_transactions WHERE reference LIKE $1", "TRF-"+userA.User.ID+"-%")
		_, _ = pool.Exec(context.Background(), "DELETE FROM accounts WHERE id IN ($1, $2)", accA.ID, accB.ID)
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id IN ($1, $2)", userA.User.ID, userB.User.ID)
	})

	// Seed accA with 150,000 VND.
	if _, err := pool.Exec(context.Background(), "UPDATE accounts SET balance_amount = 150000 WHERE id = $1", accA.ID); err != nil {
		t.Fatalf("seed balance: %v", err)
	}

	// 1. Transfer 50,000 VND -> 201 Created
	body := fmt.Sprintf(`{"source_account_id":%q,"destination_account_id":%q,"amount":50000,"currency":"VND"}`, accA.ID, accB.ID)
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(body))
	req1.Header.Set("Authorization", "Bearer "+userA.AccessToken)
	req1.Header.Set("Idempotency-Key", "idem-first")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec1.Code, rec1.Body.String())
	}
	var tr1 transferdb.Transfer
	if err := json.Unmarshal(rec1.Body.Bytes(), &tr1); err != nil {
		t.Fatalf("unmarshal transfer: %v", err)
	}

	// 2. Idempotent replay (same key + same payload) -> 200 OK, same ID
	reqReplay := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(body))
	reqReplay.Header.Set("Authorization", "Bearer "+userA.AccessToken)
	reqReplay.Header.Set("Idempotency-Key", "idem-first")
	recReplay := httptest.NewRecorder()
	router.ServeHTTP(recReplay, reqReplay)
	if recReplay.Code != http.StatusOK {
		t.Fatalf("expected 200 on replay, got %d", recReplay.Code)
	}
	var trReplay transferdb.Transfer
	_ = json.Unmarshal(recReplay.Body.Bytes(), &trReplay)
	if trReplay.ID != tr1.ID {
		t.Fatalf("expected same transfer ID on replay, got %s vs %s", trReplay.ID, tr1.ID)
	}

	// 3. Idempotency conflict (same key + different amount) -> 409 Conflict
	diffBody := fmt.Sprintf(`{"source_account_id":%q,"destination_account_id":%q,"amount":60000,"currency":"VND"}`, accA.ID, accB.ID)
	reqConflict := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(diffBody))
	reqConflict.Header.Set("Authorization", "Bearer "+userA.AccessToken)
	reqConflict.Header.Set("Idempotency-Key", "idem-first")
	recConflict := httptest.NewRecorder()
	router.ServeHTTP(recConflict, reqConflict)
	if recConflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 on idempotency conflict, got %d", recConflict.Code)
	}

	// 4. 100 concurrent transfers of 10,000 VND each from remaining 100,000 VND:
	//    Exactly 10 must succeed, 90 must fail with 422 insufficient balance.
	var okCount atomic.Int64
	var failCount atomic.Int64
	var wg sync.WaitGroup
	concBody := fmt.Sprintf(`{"source_account_id":%q,"destination_account_id":%q,"amount":10000,"currency":"VND"}`, accA.ID, accB.ID)

	for i := range 100 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/transfers", strings.NewReader(concBody))
			req.Header.Set("Authorization", "Bearer "+userA.AccessToken)
			req.Header.Set("Idempotency-Key", fmt.Sprintf("conc-%d", idx))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusCreated {
				okCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if okCount.Load() != 10 || failCount.Load() != 90 {
		t.Fatalf("expected 10 succeeded and 90 failed, got ok=%d fail=%d", okCount.Load(), failCount.Load())
	}

	balA, _ := accSvc.GetBalance(context.Background(), userA.User.ID, accA.ID)
	balB, _ := accSvc.GetBalance(context.Background(), userB.User.ID, accB.ID)
	if balA.BalanceAmount != 0 || balB.BalanceAmount != 150000 {
		t.Fatalf("balance conservation violated: accA=%d accB=%d", balA.BalanceAmount, balB.BalanceAmount)
	}
}
