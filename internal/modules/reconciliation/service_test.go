package reconciliation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreateRun_Validation(t *testing.T) {
	svc := NewService(nil)
	now := time.Now().UTC()

	tests := []struct {
		name    string
		in      CreateRunInput
		wantErr error
	}{
		{
			name: "missing bank_code",
			in: CreateRunInput{
				UserID:      "u1",
				BankCode:    " ",
				Currency:    "VND",
				PeriodStart: now.Add(-time.Hour),
				PeriodEnd:   now,
			},
			wantErr: ErrInvalidBankCode,
		},
		{
			name: "unsupported currency",
			in: CreateRunInput{
				UserID:      "u1",
				BankCode:    "VCB",
				Currency:    "EUR",
				PeriodStart: now.Add(-time.Hour),
				PeriodEnd:   now,
			},
			wantErr: ErrUnsupportedCurrency,
		},
		{
			name: "period_end before period_start",
			in: CreateRunInput{
				UserID:      "u1",
				BankCode:    "VCB",
				Currency:    "VND",
				PeriodStart: now,
				PeriodEnd:   now.Add(-time.Minute),
			},
			wantErr: ErrInvalidPeriod,
		},
		{
			name: "duplicate external_ref in statement",
			in: CreateRunInput{
				UserID:      "u1",
				BankCode:    "VCB",
				Currency:    "VND",
				PeriodStart: now.Add(-time.Hour),
				PeriodEnd:   now,
				Records: []BankStatementRecord{
					{ExternalRef: "REF-1", Direction: "deposit", Amount: 1000, Currency: "VND", Status: "completed"},
					{ExternalRef: "REF-1", Direction: "deposit", Amount: 1000, Currency: "VND", Status: "completed"},
				},
			},
			wantErr: ErrDuplicateExternalRef,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreateRun(context.Background(), tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveItem_Validation(t *testing.T) {
	svc := NewService(nil)

	tests := []struct {
		name    string
		in      ResolveItemInput
		wantErr error
	}{
		{
			name: "invalid status open",
			in: ResolveItemInput{
				UserID:           "u1",
				RunID:            "r1",
				ItemID:           "i1",
				ResolutionStatus: "open",
				ResolutionNote:   "note",
			},
			wantErr: ErrInvalidResolution,
		},
		{
			name: "invalid status not_applicable",
			in: ResolveItemInput{
				UserID:           "u1",
				RunID:            "r1",
				ItemID:           "i1",
				ResolutionStatus: "not_applicable",
				ResolutionNote:   "note",
			},
			wantErr: ErrInvalidResolution,
		},
		{
			name: "empty note",
			in: ResolveItemInput{
				UserID:           "u1",
				RunID:            "r1",
				ItemID:           "i1",
				ResolutionStatus: "resolved",
				ResolutionNote:   "   ",
			},
			wantErr: ErrInvalidResolution,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ResolveItem(context.Background(), tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}
}
