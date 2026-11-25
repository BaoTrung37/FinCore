package payment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/modules/payment"
)

func TestComputeRequestHash_DeterministicAndCanonical(t *testing.T) {
	h1 := payment.ComputeRequestHash("deposit", "acc-1", 50000, "vnd", "mock_vcb", "1234567890")
	h2 := payment.ComputeRequestHash(" DEPOSIT ", " acc-1 ", 50000, "VND", "MOCK_VCB", " 1234567890 ")
	if h1 != h2 {
		t.Fatalf("expected canonical hashes to match: %s vs %s", h1, h2)
	}

	h3 := payment.ComputeRequestHash("withdrawal", "acc-1", 50000, "VND", "MOCK_VCB", "1234567890")
	if h1 == h3 {
		t.Fatal("expected different direction to produce different hash")
	}
}

func TestMockBankGateway_DeterministicStatuses(t *testing.T) {
	gw := payment.NewMockBankGateway()
	ctx := context.Background()

	okResp, err := gw.InitiateDeposit(ctx, payment.BankRequest{
		Direction:         "deposit",
		BankCode:          "MOCK_VCB",
		BankAccountNumber: "9988776655",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if okResp.Status != "completed" || okResp.ExternalRef == "" {
		t.Fatalf("expected completed with external ref, got %+v", okResp)
	}

	failResp, err := gw.InitiateWithdrawal(ctx, payment.BankRequest{
		Direction:         "withdrawal",
		BankCode:          "MOCK_FAIL",
		BankAccountNumber: "9988776655",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failResp.Status != "failed" || failResp.FailureReason == "" {
		t.Fatalf("expected failed with reason, got %+v", failResp)
	}

	pendResp, err := gw.InitiateDeposit(ctx, payment.BankRequest{
		Direction:         "deposit",
		BankCode:          "MOCK_PENDING",
		BankAccountNumber: "9988776655",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pendResp.Status != "pending" || pendResp.ExternalRef == "" {
		t.Fatalf("expected pending with external ref, got %+v", pendResp)
	}
}

func TestVerifyCallbackSignature(t *testing.T) {
	secret := "super-secret-mock-bank-key"
	payload := []byte(`{"external_ref":"MDEP-123","status":"completed"}`)
	sig := payment.ComputeCallbackSignature(secret, payload)

	if !payment.VerifyCallbackSignature(secret, payload, sig) {
		t.Fatal("expected valid signature to verify")
	}
	if payment.VerifyCallbackSignature(secret, payload, "bad-sig") {
		t.Fatal("expected invalid signature to fail")
	}
	if !payment.VerifyCallbackSignature("", payload, "") {
		t.Fatal("expected empty secret to skip signature check")
	}
}

func TestService_InputValidation(t *testing.T) {
	svc := payment.NewService(nil, nil, nil, nil)
	ctx := context.Background()

	_, err := svc.InitiateDeposit(ctx, payment.CreatePaymentInput{
		UserID:            "u1",
		AccountID:         "a1",
		Amount:            100,
		Currency:          "VND",
		BankCode:          "MOCK_VCB",
		BankAccountNumber: "123456",
	})
	if !errors.Is(err, payment.ErrMissingIdempotencyKey) {
		t.Fatalf("expected ErrMissingIdempotencyKey, got %v", err)
	}

	_, err = svc.InitiateWithdrawal(ctx, payment.CreatePaymentInput{
		UserID:            "u1",
		AccountID:         "a1",
		IdempotencyKey:    "k1",
		Amount:            0,
		Currency:          "VND",
		BankCode:          "MOCK_VCB",
		BankAccountNumber: "123456",
	})
	if !errors.Is(err, payment.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}

	_, err = svc.InitiateDeposit(ctx, payment.CreatePaymentInput{
		UserID:            "u1",
		AccountID:         "a1",
		IdempotencyKey:    "k1",
		Amount:            100,
		Currency:          "EUR",
		BankCode:          "MOCK_VCB",
		BankAccountNumber: "123456",
	})
	if !errors.Is(err, payment.ErrUnsupportedCurrency) {
		t.Fatalf("expected ErrUnsupportedCurrency, got %v", err)
	}
}
