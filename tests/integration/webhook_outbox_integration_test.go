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
	"sync/atomic"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/account"
	accountdb "github.com/BaoTrung37/FinCore/internal/modules/account/db"
	"github.com/BaoTrung37/FinCore/internal/modules/auth"
	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
	"github.com/BaoTrung37/FinCore/internal/modules/payment"
	"github.com/BaoTrung37/FinCore/internal/modules/webhook"
	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestWebhookOutbox_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://fincore_app:local_dev_password@localhost:5432/fincore?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.NewPostgresPool(ctx, dsn)
	if err != nil {
		t.Skipf("postgres unreachable; skipping integration test: %v", err)
	}
	defer pool.Close()

	if err := database.MigrateUp(dsn, migrations.FS); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	var receivedCount atomic.Int32
	var lastSig, lastEvent string
	var lastBody []byte
	failNext := true

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if failNext {
			failNext = false
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		lastSig = r.Header.Get("X-FinCore-Signature")
		lastEvent = r.Header.Get("X-FinCore-Event")
		lastBody = body
		receivedCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tm := auth.NewTokenManager("integration-test-secret-32-bytes!!", time.Hour)
	authSvc := auth.NewService(auth.NewPostgresRepository(pool), tm)
	ledgerSvc := ledger.NewService(pool)
	accSvc := account.NewService(accountdb.New(pool))
	webhookSvc := webhook.NewService(pool)
	paySvc := payment.NewService(pool, accSvc, ledgerSvc, payment.NewMockBankGateway())
	paySvc.SetOutboxPublisher(webhookSvc)

	router := httpserver.NewRouter(log, pool)
	auth.NewHandler(authSvc, log).RegisterRoutes(router, tm)
	account.NewHandler(accSvc, log, ledgerSvc).RegisterRoutes(router, tm)
	payment.NewHandler(paySvc, log, "integration-mock-bank-secret").RegisterRoutes(router, tm)
	webhook.NewHandler(webhookSvc, log).RegisterRoutes(router, tm)

	email := fmt.Sprintf("wh-%d@example.com", time.Now().UnixNano())
	user, err := authSvc.Register(context.Background(), email, "StrongPass123!")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	tok, err := tm.Generate(user.User.ID, user.User.Email)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	// 1. Register webhook endpoint
	whBody := fmt.Sprintf(`{"url":%q,"events":["payment.completed"]}`, receiver.URL)
	reqWH := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(whBody))
	reqWH.Header.Set("Authorization", "Bearer "+tok)
	recWH := httptest.NewRecorder()
	router.ServeHTTP(recWH, reqWH)
	if recWH.Code != http.StatusCreated {
		t.Fatalf("expected 201 creating webhook, got %d (%s)", recWH.Code, recWH.Body.String())
	}
	var whResp struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(recWH.Body).Decode(&whResp); err != nil {
		t.Fatalf("decode webhook response: %v", err)
	}

	// 2. Create account and perform instant deposit -> inserts outbox_events row
	acc, err := accSvc.CreateAccount(context.Background(), user.User.ID, "VND")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	depBody := fmt.Sprintf(`{"account_id":%q,"amount":250000,"currency":"VND","bank_code":"MOCK_VCB","bank_account_number":"1234567890"}`, acc.ID)
	reqDep := httptest.NewRequest(http.MethodPost, "/api/v1/payments/deposits", strings.NewReader(depBody))
	reqDep.Header.Set("Authorization", "Bearer "+tok)
	reqDep.Header.Set("Idempotency-Key", "wh-dep-1")
	recDep := httptest.NewRecorder()
	router.ServeHTTP(recDep, reqDep)
	if recDep.Code != http.StatusCreated {
		t.Fatalf("expected 201 on deposit, got %d (%s)", recDep.Code, recDep.Body.String())
	}

	var payResp struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(recDep.Body).Decode(&payResp); err != nil {
		t.Fatalf("decode deposit: %v", err)
	}

	worker := webhook.NewRelayWorker(pool, log, time.Second)

	// 3. First batch: receiver returns 500 -> event stays pending with attempts=1
	if err := worker.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("first ProcessBatch: %v", err)
	}
	if receivedCount.Load() != 0 {
		t.Fatalf("expected 0 successful deliveries after 500, got %d", receivedCount.Load())
	}

	// Force next_retry_at <= now() for immediate retry in test
	if _, err := pool.Exec(context.Background(), `UPDATE outbox_events SET next_retry_at = now() - interval '1 second' WHERE aggregate_id = $1`, payResp.ID); err != nil {
		t.Fatalf("reset next_retry_at: %v", err)
	}

	// 4. Second batch: receiver returns 200 -> event becomes completed & signature verifies
	if err := worker.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("second ProcessBatch: %v", err)
	}
	if receivedCount.Load() != 1 {
		t.Fatalf("expected 1 successful delivery, got %d", receivedCount.Load())
	}
	if lastEvent != "payment.completed" {
		t.Fatalf("expected event payment.completed, got %q", lastEvent)
	}
	if !webhook.VerifySignature(whResp.Secret, lastBody, lastSig) {
		t.Fatal("expected valid HMAC signature on delivered webhook")
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM outbox_events WHERE aggregate_id = $1`, payResp.ID).Scan(&status); err != nil {
		t.Fatalf("query outbox status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("expected outbox status completed, got %q", status)
	}
}
