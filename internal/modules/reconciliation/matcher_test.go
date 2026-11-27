package reconciliation

import (
	"errors"
	"strings"
	"testing"
)

func TestMatchRecords_AllOutcomes(t *testing.T) {
	bank := []BankStatementRecord{
		{ExternalRef: "REF-MATCH", Direction: "deposit", Amount: 100000, Currency: "VND", Status: "completed"},
		{ExternalRef: "REF-AMT", Direction: "deposit", Amount: 250000, Currency: "VND", Status: "completed"},
		{ExternalRef: "REF-DIR", Direction: "withdrawal", Amount: 50000, Currency: "VND", Status: "completed"},
		{ExternalRef: "REF-STAT", Direction: "deposit", Amount: 75000, Currency: "VND", Status: "completed"},
		{ExternalRef: "REF-BANK-ONLY", Direction: "deposit", Amount: 30000, Currency: "VND", Status: "completed"},
	}
	internal := []InternalPaymentRecord{
		{PaymentID: "pay-1", ExternalRef: "REF-MATCH", Direction: "deposit", Amount: 100000, Currency: "VND", Status: "completed"},
		{PaymentID: "pay-2", ExternalRef: "REF-AMT", Direction: "deposit", Amount: 200000, Currency: "VND", Status: "completed"},
		{PaymentID: "pay-3", ExternalRef: "REF-DIR", Direction: "deposit", Amount: 50000, Currency: "VND", Status: "completed"},
		{PaymentID: "pay-4", ExternalRef: "REF-STAT", Direction: "deposit", Amount: 75000, Currency: "VND", Status: "pending"},
		{PaymentID: "pay-5", ExternalRef: "REF-INTERNAL-ONLY", Direction: "withdrawal", Amount: 90000, Currency: "VND", Status: "completed"},
	}

	items, summary, err := MatchRecords("VND", bank, internal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.TotalBankRecords != 5 {
		t.Fatalf("expected 5 bank records, got %d", summary.TotalBankRecords)
	}
	if summary.TotalInternalRecords != 5 {
		t.Fatalf("expected 5 internal records, got %d", summary.TotalInternalRecords)
	}
	if summary.MatchedCount != 1 {
		t.Fatalf("expected 1 matched, got %d", summary.MatchedCount)
	}
	if summary.DiscrepancyCount != 5 {
		t.Fatalf("expected 5 discrepancies, got %d", summary.DiscrepancyCount)
	}
	if summary.Status != "has_discrepancies" {
		t.Fatalf("expected status has_discrepancies, got %q", summary.Status)
	}
	if len(items) != 6 {
		t.Fatalf("expected 6 items, got %d", len(items))
	}

	want := []struct {
		ref        string
		match      string
		resolution string
	}{
		{"REF-MATCH", "matched", "not_applicable"},
		{"REF-AMT", "amount_mismatch", "open"},
		{"REF-DIR", "direction_mismatch", "open"},
		{"REF-STAT", "status_mismatch", "open"},
		{"REF-BANK-ONLY", "missing_in_fincore", "open"},
		{"REF-INTERNAL-ONLY", "missing_in_bank", "open"},
	}
	for i, w := range want {
		if items[i].ExternalRef != w.ref || items[i].MatchStatus != w.match || items[i].ResolutionStatus != w.resolution {
			t.Errorf("item[%d] = (%s, %s, %s), want (%s, %s, %s)",
				i, items[i].ExternalRef, items[i].MatchStatus, items[i].ResolutionStatus,
				w.ref, w.match, w.resolution)
		}
	}
}

func TestMatchRecords_AllMatched(t *testing.T) {
	bank := []BankStatementRecord{
		{ExternalRef: "REF-1", Direction: "deposit", Amount: 50000, Currency: "VND", Status: "completed"},
	}
	internal := []InternalPaymentRecord{
		{PaymentID: "pay-1", ExternalRef: "REF-1", Direction: "deposit", Amount: 50000, Currency: "VND", Status: "completed"},
	}

	items, summary, err := MatchRecords("VND", bank, internal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.DiscrepancyCount != 0 || summary.MatchedCount != 1 || summary.Status != "completed" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(items) != 1 || items[0].MatchStatus != "matched" || items[0].ResolutionStatus != "not_applicable" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestMatchRecords_EmptyBankStatement(t *testing.T) {
	internal := []InternalPaymentRecord{
		{PaymentID: "pay-1", ExternalRef: "REF-1", Direction: "deposit", Amount: 10000, Currency: "VND", Status: "completed"},
		{PaymentID: "pay-2", ExternalRef: "REF-2", Direction: "withdrawal", Amount: 20000, Currency: "VND", Status: "failed"},
	}

	items, summary, err := MatchRecords("VND", nil, internal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.TotalBankRecords != 0 || summary.TotalInternalRecords != 2 || summary.DiscrepancyCount != 2 || summary.Status != "has_discrepancies" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(items) != 2 || items[0].MatchStatus != "missing_in_bank" || items[1].MatchStatus != "missing_in_bank" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestMatchRecords_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		bank     []BankStatementRecord
		wantErr  error
	}{
		{
			name:     "unsupported run currency",
			currency: "EUR",
			bank:     nil,
			wantErr:  ErrUnsupportedCurrency,
		},
		{
			name:     "duplicate external_ref",
			currency: "VND",
			bank: []BankStatementRecord{
				{ExternalRef: "REF-DUP", Direction: "deposit", Amount: 1000, Currency: "VND", Status: "completed"},
				{ExternalRef: "REF-DUP", Direction: "deposit", Amount: 1000, Currency: "VND", Status: "completed"},
			},
			wantErr: ErrDuplicateExternalRef,
		},
		{
			name:     "empty external_ref",
			currency: "VND",
			bank:     []BankStatementRecord{{ExternalRef: " ", Direction: "deposit", Amount: 1000, Currency: "VND", Status: "completed"}},
			wantErr:  ErrInvalidStatementRecord,
		},
		{
			name:     "non-positive amount",
			currency: "VND",
			bank:     []BankStatementRecord{{ExternalRef: "REF-1", Direction: "deposit", Amount: 0, Currency: "VND", Status: "completed"}},
			wantErr:  ErrInvalidStatementRecord,
		},
		{
			name:     "currency mismatch",
			currency: "VND",
			bank:     []BankStatementRecord{{ExternalRef: "REF-1", Direction: "deposit", Amount: 1000, Currency: "USD", Status: "completed"}},
			wantErr:  ErrInvalidStatementRecord,
		},
		{
			name:     "invalid direction",
			currency: "VND",
			bank:     []BankStatementRecord{{ExternalRef: "REF-1", Direction: "transfer", Amount: 1000, Currency: "VND", Status: "completed"}},
			wantErr:  ErrInvalidStatementRecord,
		},
		{
			name:     "invalid status",
			currency: "VND",
			bank:     []BankStatementRecord{{ExternalRef: "REF-1", Direction: "deposit", Amount: 1000, Currency: "VND", Status: "reversed"}},
			wantErr:  ErrInvalidStatementRecord,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := MatchRecords(tc.currency, tc.bank, nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestParseStatementCSV_Valid(t *testing.T) {
	raw := "external_ref,direction,amount,currency,status\n" +
		"REF-1,deposit,100000,VND,completed\n" +
		"REF-2,withdrawal,50000,VND,failed\n"

	records, err := ParseStatementCSV(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].ExternalRef != "REF-1" || records[0].Amount != 100000 || records[0].Direction != "deposit" || records[0].Currency != "VND" || records[0].Status != "completed" {
		t.Fatalf("unexpected record[0]: %+v", records[0])
	}
	if records[1].ExternalRef != "REF-2" || records[1].Amount != 50000 || records[1].Direction != "withdrawal" || records[1].Status != "failed" {
		t.Fatalf("unexpected record[1]: %+v", records[1])
	}
}

func TestParseStatementCSV_Errors(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{
			name:    "empty reader",
			raw:     "",
			wantErr: ErrInvalidCSVFormat,
		},
		{
			name:    "wrong header",
			raw:     "ref,type,amt,curr,st\nREF-1,deposit,1000,VND,completed\n",
			wantErr: ErrInvalidCSVFormat,
		},
		{
			name:    "decimal amount rejected",
			raw:     "external_ref,direction,amount,currency,status\nREF-1,deposit,100.50,VND,completed\n",
			wantErr: ErrInvalidStatementRecord,
		},
		{
			name:    "negative amount rejected",
			raw:     "external_ref,direction,amount,currency,status\nREF-1,deposit,-500,VND,completed\n",
			wantErr: ErrInvalidStatementRecord,
		},
		{
			name:    "zero amount rejected",
			raw:     "external_ref,direction,amount,currency,status\nREF-1,deposit,0,VND,completed\n",
			wantErr: ErrInvalidStatementRecord,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseStatementCSV(strings.NewReader(tc.raw))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}
}
