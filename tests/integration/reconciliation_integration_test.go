package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
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
	"github.com/BaoTrung37/FinCore/internal/modules/reconciliation"
	"github.com/BaoTrung37/FinCore/internal/modules/webhook"
	"github.com/BaoTrung37/FinCore/internal/platform/database"
	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/BaoTrung37/FinCore/migrations"
)

func TestReconciliation_Integration(t *testing.T) {
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

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	tm := auth.NewTokenManager("integration-test-secret-32-bytes!!", time.Hour)
	authSvc := auth.NewService(auth.NewPostgresRepository(pool), tm)
	ledgerSvc := ledger.NewService(pool)
	accSvc := account.NewService(accountdb.New(pool))
	webhookSvc := webhook.NewService(pool)
	paySvc := payment.NewService(pool, accSvc, ledgerSvc, payment.NewMockBankGateway())
	paySvc.SetOutboxPublisher(webhookSvc)
	reconSvc := reconciliation.NewService(pool)
	reconSvc.SetOutboxPublisher(webhookSvc)

	router := httpserver.NewRouter(log, pool)
	auth.NewHandler(authSvc, log).RegisterRoutes(router, tm)
	account.NewHandler(accSvc, log, ledgerSvc).RegisterRoutes(router, tm)
	payment.NewHandler(paySvc, log, "integration-mock-bank-secret").RegisterRoutes(router, tm)
	webhook.NewHandler(webhookSvc, log).RegisterRoutes(router, tm)
	reconciliation.NewHandler(reconSvc, log).RegisterRoutes(router, tm)

	email := fmt.Sprintf("recon-%d@example.com", time.Now().UnixNano())
	reg, err := authSvc.Register(context.Background(), email, "StrongPass123!")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	tok, err := tm.Generate(reg.User.ID, reg.User.Email)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	acc, err := accSvc.CreateAccount(context.Background(), reg.User.ID, "VND")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	periodStart := time.Now().UTC().Add(-time.Minute)

	// Create 3 deposits via payment service, settle 2 as completed, leave 1 pending.
	dep1, err := paySvc.InitiateDeposit(context.Background(), payment.CreatePaymentInput{
		UserID:            reg.User.ID,
		AccountID:         acc.ID,
		IdempotencyKey:    fmt.Sprintf("dep1-%d", time.Now().UnixNano()),
		Amount:            100000,
		Currency:          "VND",
		BankCode:          "VCB",
		BankAccountNumber: "0011223344",
	})
	if err != nil {
		t.Fatalf("create deposit 1: %v", err)
	}
	if _, err := paySvc.HandleBankCallback(context.Background(), payment.BankCallbackInput{
		ExternalRef: dep1.Payment.ExternalRef.String,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("settle deposit 1: %v", err)
	}

	dep2, err := paySvc.InitiateDeposit(context.Background(), payment.CreatePaymentInput{
		UserID:            reg.User.ID,
		AccountID:         acc.ID,
		IdempotencyKey:    fmt.Sprintf("dep2-%d", time.Now().UnixNano()),
		Amount:            200000,
		Currency:          "VND",
		BankCode:          "VCB",
		BankAccountNumber: "0011223344",
	})
	if err != nil {
		t.Fatalf("create deposit 2: %v", err)
	}
	if _, err := paySvc.HandleBankCallback(context.Background(), payment.BankCallbackInput{
		ExternalRef: dep2.Payment.ExternalRef.String,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("settle deposit 2: %v", err)
	}

	dep3, err := paySvc.InitiateDeposit(context.Background(), payment.CreatePaymentInput{
		UserID:            reg.User.ID,
		AccountID:         acc.ID,
		IdempotencyKey:    fmt.Sprintf("dep3-%d", time.Now().UnixNano()),
		Amount:            50000,
		Currency:          "VND",
		BankCode:          "VCB",
		BankAccountNumber: "0011223344",
	})
	if err != nil {
		t.Fatalf("create deposit 3: %v", err)
	}

	periodEnd := time.Now().UTC().Add(time.Minute)

	// 1. JSON reconciliation run: 1 matched, 1 amount_mismatch, 1 missing_in_fincore, 1 missing_in_bank.
	runPayload := map[string]any{
		"bank_code":    "VCB",
		"currency":     "VND",
		"period_start": periodStart.Format(time.RFC3339Nano),
		"period_end":   periodEnd.Format(time.RFC3339Nano),
		"records": []map[string]any{
			{"external_ref": dep1.Payment.ExternalRef.String, "direction": "deposit", "amount": 100000, "currency": "VND", "status": "completed"},
			{"external_ref": dep2.Payment.ExternalRef.String, "direction": "deposit", "amount": 250000, "currency": "VND", "status": "completed"},
			{"external_ref": "BANK-UNKNOWN-999", "direction": "deposit", "amount": 30000, "currency": "VND", "status": "completed"},
		},
	}
	bodyBytes, _ := json.Marshal(runPayload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reconciliation/runs", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for JSON reconciliation run, got %d (%s)", rec.Code, rec.Body.String())
	}

	var runResp struct {
		ID               string `json:"id"`
		Status           string `json:"status"`
		MatchedCount     int32  `json:"matched_count"`
		DiscrepancyCount int32  `json:"discrepancy_count"`
		Items            []struct {
			ID               string `json:"id"`
			ExternalRef      string `json:"external_ref"`
			MatchStatus      string `json:"match_status"`
			ResolutionStatus string `json:"resolution_status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if runResp.Status != "has_discrepancies" || runResp.MatchedCount != 1 || runResp.DiscrepancyCount != 3 {
		t.Fatalf("unexpected run summary: %+v", runResp)
	}

	// 2. Filter items by match_status=amount_mismatch.
	reqFilter := httptest.NewRequest(http.MethodGet, "/api/v1/reconciliation/runs/"+runResp.ID+"?match_status=amount_mismatch", nil)
	reqFilter.Header.Set("Authorization", "Bearer "+tok)
	recFilter := httptest.NewRecorder()
	router.ServeHTTP(recFilter, reqFilter)
	if recFilter.Code != http.StatusOK {
		t.Fatalf("expected 200 filtering run items, got %d (%s)", recFilter.Code, recFilter.Body.String())
	}
	var filtered struct {
		Items []struct {
			ID          string `json:"id"`
			MatchStatus string `json:"match_status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(recFilter.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered run: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].MatchStatus != "amount_mismatch" {
		t.Fatalf("expected 1 amount_mismatch item, got %+v", filtered.Items)
	}

	// 3. Resolve the amount_mismatch item -> 200; attempt to resolve matched item -> 400.
	mismatchItemID := filtered.Items[0].ID
	var matchedItemID string
	for _, it := range runResp.Items {
		if it.MatchStatus == "matched" {
			matchedItemID = it.ID
			break
		}
	}

	reqResolve := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/reconciliation/runs/"+runResp.ID+"/items/"+mismatchItemID,
		strings.NewReader(`{"resolution_status":"resolved","resolution_note":"bank fee verified"}`),
	)
	reqResolve.Header.Set("Authorization", "Bearer "+tok)
	reqResolve.Header.Set("Content-Type", "application/json")
	recResolve := httptest.NewRecorder()
	router.ServeHTTP(recResolve, reqResolve)
	if recResolve.Code != http.StatusOK {
		t.Fatalf("expected 200 resolving mismatch item, got %d (%s)", recResolve.Code, recResolve.Body.String())
	}

	reqResolveMatched := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/reconciliation/runs/"+runResp.ID+"/items/"+matchedItemID,
		strings.NewReader(`{"resolution_status":"resolved","resolution_note":"should fail"}`),
	)
	reqResolveMatched.Header.Set("Authorization", "Bearer "+tok)
	reqResolveMatched.Header.Set("Content-Type", "application/json")
	recResolveMatched := httptest.NewRecorder()
	router.ServeHTTP(recResolveMatched, reqResolveMatched)
	if recResolveMatched.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 resolving matched item, got %d (%s)", recResolveMatched.Code, recResolveMatched.Body.String())
	}

	// 4. CSV reconciliation run matching all 3 payments -> completed.
	csvContent := fmt.Sprintf(
		"external_ref,direction,amount,currency,status\n%s,deposit,100000,VND,completed\n%s,deposit,200000,VND,completed\n%s,deposit,50000,VND,completed\n",
		dep1.Payment.ExternalRef.String,
		dep2.Payment.ExternalRef.String,
		dep3.Payment.ExternalRef.String,
	)
	var csvBuf bytes.Buffer
	mw := multipart.NewWriter(&csvBuf)
	_ = mw.WriteField("bank_code", "VCB")
	_ = mw.WriteField("currency", "VND")
	_ = mw.WriteField("period_start", periodStart.Format(time.RFC3339Nano))
	_ = mw.WriteField("period_end", periodEnd.Format(time.RFC3339Nano))
	part, _ := mw.CreateFormFile("file", "vcb.csv")
	_, _ = part.Write([]byte(csvContent))
	_ = mw.Close()

	reqCSV := httptest.NewRequest(http.MethodPost, "/api/v1/reconciliation/runs/csv", &csvBuf)
	reqCSV.Header.Set("Authorization", "Bearer "+tok)
	reqCSV.Header.Set("Content-Type", mw.FormDataContentType())
	recCSV := httptest.NewRecorder()
	router.ServeHTTP(recCSV, reqCSV)
	if recCSV.Code != http.StatusCreated {
		t.Fatalf("expected 201 for CSV reconciliation run, got %d (%s)", recCSV.Code, recCSV.Body.String())
	}
	var csvRun struct {
		Status           string `json:"status"`
		MatchedCount     int32  `json:"matched_count"`
		DiscrepancyCount int32  `json:"discrepancy_count"`
	}
	if err := json.Unmarshal(recCSV.Body.Bytes(), &csvRun); err != nil {
		t.Fatalf("decode CSV run: %v", err)
	}
	if csvRun.Status != "completed" || csvRun.MatchedCount != 3 || csvRun.DiscrepancyCount != 0 {
		t.Fatalf("unexpected CSV run result: %+v", csvRun)
	}

	// 5. Internal Ledger Integrity Audit -> 0 unbalanced transactions and our account has no drift.
	reqAudit := httptest.NewRequest(http.MethodPost, "/api/v1/reconciliation/ledger-audit", nil)
	reqAudit.Header.Set("Authorization", "Bearer "+tok)
	recAudit := httptest.NewRecorder()
	router.ServeHTTP(recAudit, reqAudit)
	if recAudit.Code != http.StatusCreated {
		t.Fatalf("expected 201 for ledger audit, got %d (%s)", recAudit.Code, recAudit.Body.String())
	}
	var auditResp struct {
		ID                     string                         `json:"id"`
		Status                 string                         `json:"status"`
		CheckedTransactions    int32                          `json:"checked_transactions"`
		UnbalancedTransactions int32                          `json:"unbalanced_transactions"`
		CheckedAccounts        int32                          `json:"checked_accounts"`
		DriftedAccounts        int32                          `json:"drifted_accounts"`
		Anomalies              []reconciliation.LedgerAnomaly `json:"anomalies"`
	}
	if err := json.Unmarshal(recAudit.Body.Bytes(), &auditResp); err != nil {
		t.Fatalf("decode ledger audit: %v", err)
	}
	if auditResp.Status != "healthy" || auditResp.UnbalancedTransactions != 0 || auditResp.DriftedAccounts != 0 || auditResp.CheckedTransactions != 3 || auditResp.CheckedAccounts != 1 {
		t.Fatalf("expected tenant-scoped healthy ledger audit (3 txns, 1 account), got: %+v", auditResp)
	}

	reqListAudit := httptest.NewRequest(http.MethodGet, "/api/v1/reconciliation/ledger-audit", nil)
	reqListAudit.Header.Set("Authorization", "Bearer "+tok)
	recListAudit := httptest.NewRecorder()
	router.ServeHTTP(recListAudit, reqListAudit)
	if recListAudit.Code != http.StatusOK {
		t.Fatalf("expected 200 listing ledger audits, got %d (%s)", recListAudit.Code, recListAudit.Body.String())
	}
	var auditList []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recListAudit.Body.Bytes(), &auditList); err != nil {
		t.Fatalf("decode ledger audit list: %v", err)
	}
	if len(auditList) == 0 || auditList[0].ID != auditResp.ID {
		t.Fatalf("expected latest ledger audit %s in list, got %+v", auditResp.ID, auditList)
	}

	// 6. Verify outbox events were published atomically.
	var reconEvents, auditEvents int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox_events WHERE user_id = $1 AND event_type = 'reconciliation.run_completed'`,
		reg.User.ID,
	).Scan(&reconEvents); err != nil {
		t.Fatalf("query reconciliation outbox events: %v", err)
	}
	if reconEvents < 2 {
		t.Fatalf("expected at least 2 reconciliation.run_completed outbox events, got %d", reconEvents)
	}

	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM outbox_events WHERE user_id = $1 AND event_type = 'reconciliation.ledger_audit_completed'`,
		reg.User.ID,
	).Scan(&auditEvents); err != nil {
		t.Fatalf("query ledger audit outbox events: %v", err)
	}
	if auditEvents < 1 {
		t.Fatalf("expected at least 1 reconciliation.ledger_audit_completed outbox event, got %d", auditEvents)
	}
}
