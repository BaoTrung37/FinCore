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
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	"github.com/BaoTrung37/FinCore/internal/modules/payment"
	paymentdb "github.com/BaoTrung37/FinCore/internal/modules/payment/db"
	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestPayment_DepositWithdrawalCallbackAndIdempotency_Integration(t *testing.T) {
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

	const webhookSecret = "integration-mock-bank-secret"
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tm := auth.NewTokenManager("integration-test-secret-32-bytes!!", time.Hour)
	authSvc := auth.NewService(auth.NewPostgresRepository(pool), tm)
	ledgerSvc := ledger.NewService(pool)
	accSvc := account.NewService(accountdb.New(pool))
	paySvc := payment.NewService(pool, accSvc, ledgerSvc, payment.NewMockBankGateway())

	router := httpserver.NewRouter(log, pool)
	auth.NewHandler(authSvc, log).RegisterRoutes(router, tm)
	account.NewHandler(accSvc, log, ledgerSvc).RegisterRoutes(router, tm)
	payment.NewHandler(paySvc, log, webhookSecret).RegisterRoutes(router, tm)

	email := fmt.Sprintf("pay-%d@example.com", time.Now().UnixNano())
	user, err := authSvc.Register(context.Background(), email, "StrongPass123!")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	tok, err := tm.Generate(user.ID, user.Email)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	acc, err := accSvc.CreateAccount(context.Background(), user.ID, "VND")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	// 1. Instant deposit of 500,000 VND -> 201 Created, status completed, balance = 500,000
	depBody := fmt.Sprintf(`{"account_id":%q,"amount":500000,"currency":"VND","bank_code":"MOCK_VCB","bank_account_number":"1234567890"}`, acc.ID)
	reqDep := httptest.NewRequest(http.MethodPost, "/api/v1/payments/deposits", strings.NewReader(depBody))
	reqDep.Header.Set("Authorization", "Bearer "+tok)
	reqDep.Header.Set("Idempotency-Key", "dep-key-1")
	recDep := httptest.NewRecorder()
	router.ServeHTTP(recDep, reqDep)
	if recDep.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", recDep.Code, recDep.Body.String())
	}

	var depResp paymentdb.Payment
	if err := json.NewDecoder(recDep.Body).Decode(&depResp); err != nil {
		t.Fatalf("decode deposit: %v", err)
	}
	if depResp.Status != "completed" || !depResp.LedgerTransactionID.Valid {
		t.Fatalf("expected completed deposit with ledger tx, got %+v", depResp)
	}

	// 2. Idempotent replay -> 200 OK, same payment ID
	reqReplay := httptest.NewRequest(http.MethodPost, "/api/v1/payments/deposits", strings.NewReader(depBody))
	reqReplay.Header.Set("Authorization", "Bearer "+tok)
	reqReplay.Header.Set("Idempotency-Key", "dep-key-1")
	recReplay := httptest.NewRecorder()
	router.ServeHTTP(recReplay, reqReplay)
	if recReplay.Code != http.StatusOK {
		t.Fatalf("expected 200 on replay, got %d", recReplay.Code)
	}

	// 3. Idempotency conflict -> 409 Conflict
	conflictBody := fmt.Sprintf(`{"account_id":%q,"amount":999999,"currency":"VND","bank_code":"MOCK_VCB","bank_account_number":"1234567890"}`, acc.ID)
	reqConflict := httptest.NewRequest(http.MethodPost, "/api/v1/payments/deposits", strings.NewReader(conflictBody))
	reqConflict.Header.Set("Authorization", "Bearer "+tok)
	reqConflict.Header.Set("Idempotency-Key", "dep-key-1")
	recConflict := httptest.NewRecorder()
	router.ServeHTTP(recConflict, reqConflict)
	if recConflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 on conflict, got %d", recConflict.Code)
	}

	// 4. Instant withdrawal of 150,000 VND -> 201 Created, balance becomes 350,000
	wdrBody := fmt.Sprintf(`{"account_id":%q,"amount":150000,"currency":"VND","bank_code":"MOCK_VCB","bank_account_number":"1234567890"}`, acc.ID)
	reqWdr := httptest.NewRequest(http.MethodPost, "/api/v1/payments/withdrawals", strings.NewReader(wdrBody))
	reqWdr.Header.Set("Authorization", "Bearer "+tok)
	reqWdr.Header.Set("Idempotency-Key", "wdr-key-1")
	recWdr := httptest.NewRecorder()
	router.ServeHTTP(recWdr, reqWdr)
	if recWdr.Code != http.StatusCreated {
		t.Fatalf("expected 201 on withdrawal, got %d (%s)", recWdr.Code, recWdr.Body.String())
	}

	// 5. Pending deposit + signed callback settlement (+200,000 -> balance = 550,000)
	pendBody := fmt.Sprintf(`{"account_id":%q,"amount":200000,"currency":"VND","bank_code":"MOCK_PENDING","bank_account_number":"1234567890"}`, acc.ID)
	reqPend := httptest.NewRequest(http.MethodPost, "/api/v1/payments/deposits", strings.NewReader(pendBody))
	reqPend.Header.Set("Authorization", "Bearer "+tok)
	reqPend.Header.Set("Idempotency-Key", "dep-key-pending")
	recPend := httptest.NewRecorder()
	router.ServeHTTP(recPend, reqPend)
	if recPend.Code != http.StatusCreated {
		t.Fatalf("expected 201 on pending deposit, got %d", recPend.Code)
	}
	var pendResp paymentdb.Payment
	if err := json.NewDecoder(recPend.Body).Decode(&pendResp); err != nil {
		t.Fatalf("decode pending deposit: %v", err)
	}
	if pendResp.Status != "pending" || !pendResp.ExternalRef.Valid {
		t.Fatalf("expected pending payment with external_ref, got %+v", pendResp)
	}

	cbPayload := []byte(fmt.Sprintf(`{"external_ref":%q,"status":"completed"}`, pendResp.ExternalRef.String))
	cbSig := payment.ComputeCallbackSignature(webhookSecret, cbPayload)
	reqCB := httptest.NewRequest(http.MethodPost, "/api/v1/payments/callbacks/mock-bank", strings.NewReader(string(cbPayload)))
	reqCB.Header.Set("X-Mock-Bank-Signature", cbSig)
	recCB := httptest.NewRecorder()
	router.ServeHTTP(recCB, reqCB)
	if recCB.Code != http.StatusOK {
		t.Fatalf("expected 200 on callback, got %d (%s)", recCB.Code, recCB.Body.String())
	}

	bal, err := accSvc.GetBalance(context.Background(), user.ID, acc.ID)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if bal.BalanceAmount != 550000 {
		t.Fatalf("expected final balance 550000, got %d", bal.BalanceAmount)
	}
}
