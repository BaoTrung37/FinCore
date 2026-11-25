package transfer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/modules/transfer"
)

func TestComputeRequestHash_Deterministic(t *testing.T) {
	h1 := transfer.ComputeRequestHash("acc-1", "acc-2", 10000, "VND")
	h2 := transfer.ComputeRequestHash("acc-1", "acc-2", 10000, "vnd")
	h3 := transfer.ComputeRequestHash("acc-1", "acc-2", 20000, "VND")

	if h1 == "" || h1 != h2 {
		t.Fatalf("expected deterministic normalized hash, got h1=%q h2=%q", h1, h2)
	}
	if h1 == h3 {
		t.Fatal("expected different amount to produce different hash")
	}
}

func TestService_CreateTransfer_InputValidation(t *testing.T) {
	svc := transfer.NewService(nil, nil, nil)
	ctx := context.Background()

	_, err := svc.CreateTransfer(ctx, transfer.CreateTransferInput{
		UserID:               "u1",
		IdempotencyKey:       "",
		SourceAccountID:      "a1",
		DestinationAccountID: "a2",
		Amount:               1000,
		Currency:             "VND",
	})
	if !errors.Is(err, transfer.ErrMissingIdempotencyKey) {
		t.Fatalf("expected ErrMissingIdempotencyKey, got %v", err)
	}

	_, err = svc.CreateTransfer(ctx, transfer.CreateTransferInput{
		UserID:               "u1",
		IdempotencyKey:       "key-1",
		SourceAccountID:      "a1",
		DestinationAccountID: "a1",
		Amount:               1000,
		Currency:             "VND",
	})
	if !errors.Is(err, transfer.ErrSelfTransfer) {
		t.Fatalf("expected ErrSelfTransfer, got %v", err)
	}

	_, err = svc.CreateTransfer(ctx, transfer.CreateTransferInput{
		UserID:               "u1",
		IdempotencyKey:       "key-1",
		SourceAccountID:      "a1",
		DestinationAccountID: "a2",
		Amount:               0,
		Currency:             "VND",
	})
	if !errors.Is(err, transfer.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}
