package ledger_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/BaoTrung37/FinCore/internal/modules/ledger"
)

func TestPostTransaction_ValidationInvariants(t *testing.T) {
	svc := ledger.NewService(nil)
	ctx := context.Background()

	// 1. Fewer than 2 entries
	_, _, err := svc.PostTransaction(ctx, ledger.PostTransactionInput{
		Reference:       "REF-1",
		TransactionType: "transfer",
		Entries: []ledger.EntryInput{
			{AccountID: "acc-1", EntryType: "debit", Amount: 100, Currency: "VND"},
		},
	})
	if !errors.Is(err, ledger.ErrInvalidEntries) {
		t.Fatalf("expected ErrInvalidEntries, got %v", err)
	}

	// 2. Non-positive amount
	_, _, err = svc.PostTransaction(ctx, ledger.PostTransactionInput{
		Reference:       "REF-2",
		TransactionType: "transfer",
		Entries: []ledger.EntryInput{
			{AccountID: "acc-1", EntryType: "debit", Amount: 0, Currency: "VND"},
			{AccountID: "acc-2", EntryType: "credit", Amount: 0, Currency: "VND"},
		},
	})
	if !errors.Is(err, ledger.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}

	// 3. Mixed currencies
	_, _, err = svc.PostTransaction(ctx, ledger.PostTransactionInput{
		Reference:       "REF-3",
		TransactionType: "transfer",
		Entries: []ledger.EntryInput{
			{AccountID: "acc-1", EntryType: "debit", Amount: 100, Currency: "VND"},
			{AccountID: "acc-2", EntryType: "credit", Amount: 100, Currency: "USD"},
		},
	})
	if !errors.Is(err, ledger.ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
	}

	// 4. Unbalanced debit != credit
	_, _, err = svc.PostTransaction(ctx, ledger.PostTransactionInput{
		Reference:       "REF-4",
		TransactionType: "transfer",
		Entries: []ledger.EntryInput{
			{AccountID: "acc-1", EntryType: "debit", Amount: 100, Currency: "VND"},
			{AccountID: "acc-2", EntryType: "credit", Amount: 90, Currency: "VND"},
		},
	})
	if !errors.Is(err, ledger.ErrUnbalancedTransaction) {
		t.Fatalf("expected ErrUnbalancedTransaction, got %v", err)
	}
}

func TestSortedUniqueAccountIDs(t *testing.T) {
	got := ledger.SortedUniqueAccountIDs([]ledger.EntryInput{
		{AccountID: "acc-c"},
		{AccountID: "acc-a"},
		{AccountID: "acc-c"},
		{AccountID: "acc-b"},
	})
	want := []string{"acc-a", "acc-b", "acc-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
