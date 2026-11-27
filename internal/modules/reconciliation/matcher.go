package reconciliation

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUnsupportedCurrency    = errors.New("unsupported currency")
	ErrInvalidPeriod          = errors.New("period_end must be greater than or equal to period_start")
	ErrInvalidBankCode        = errors.New("bank_code is required")
	ErrInvalidStatementRecord = errors.New("invalid bank statement record")
	ErrDuplicateExternalRef   = errors.New("duplicate external_ref in bank statement")
	ErrInvalidCSVFormat       = errors.New("invalid CSV statement format")
	ErrRunNotFound            = errors.New("reconciliation run not found")
	ErrItemNotFound           = errors.New("reconciliation item not found")
	ErrItemNotResolvable      = errors.New("matched reconciliation item cannot be resolved")
	ErrInvalidResolution      = errors.New("resolution_status must be resolved or ignored and resolution_note is required")
)

// BankStatementRecord represents one transaction line reported by an external bank.
type BankStatementRecord struct {
	ExternalRef string `json:"external_ref"`
	Direction   string `json:"direction"` // "deposit" or "withdrawal"
	Amount      int64  `json:"amount"`    // minor units (> 0)
	Currency    string `json:"currency"`  // "VND" or "USD"
	Status      string `json:"status"`    // "completed", "failed", or "pending"
}

// InternalPaymentRecord represents a FinCore payment record for reconciliation.
type InternalPaymentRecord struct {
	PaymentID   string
	ExternalRef string
	Direction   string
	Amount      int64
	Currency    string
	Status      string
}

// MatchedItem represents a single reconciliation comparison outcome.
type MatchedItem struct {
	ExternalRef       string
	PaymentID         string
	MatchStatus       string
	BankAmount        *int64
	InternalAmount    *int64
	BankDirection     string
	InternalDirection string
	BankStatus        string
	InternalStatus    string
	Currency          string
	ResolutionStatus  string
}

// MatchSummary aggregates counts and overall status of a reconciliation run.
type MatchSummary struct {
	TotalBankRecords     int32
	TotalInternalRecords int32
	MatchedCount         int32
	DiscrepancyCount     int32
	Status               string // "completed" or "has_discrepancies"
}

// MatchRecords deterministically reconciles external bank statement records against internal FinCore payments.
func MatchRecords(currency string, bankRecords []BankStatementRecord, internalPayments []InternalPaymentRecord) ([]MatchedItem, MatchSummary, error) {
	runCurrency := strings.ToUpper(strings.TrimSpace(currency))
	if runCurrency != "VND" && runCurrency != "USD" {
		return nil, MatchSummary{}, ErrUnsupportedCurrency
	}

	seenBankRefs := make(map[string]struct{}, len(bankRecords))
	normalizedBank := make([]BankStatementRecord, 0, len(bankRecords))
	for i, rec := range bankRecords {
		ref := strings.TrimSpace(rec.ExternalRef)
		if ref == "" {
			return nil, MatchSummary{}, fmt.Errorf("%w: row %d has empty external_ref", ErrInvalidStatementRecord, i+1)
		}
		if _, dup := seenBankRefs[ref]; dup {
			return nil, MatchSummary{}, fmt.Errorf("%w: %s", ErrDuplicateExternalRef, ref)
		}
		seenBankRefs[ref] = struct{}{}

		dir := strings.ToLower(strings.TrimSpace(rec.Direction))
		if dir != "deposit" && dir != "withdrawal" {
			return nil, MatchSummary{}, fmt.Errorf("%w: row %d invalid direction %q", ErrInvalidStatementRecord, i+1, rec.Direction)
		}
		if rec.Amount <= 0 {
			return nil, MatchSummary{}, fmt.Errorf("%w: row %d amount must be positive", ErrInvalidStatementRecord, i+1)
		}
		curr := strings.ToUpper(strings.TrimSpace(rec.Currency))
		if curr != runCurrency {
			return nil, MatchSummary{}, fmt.Errorf("%w: row %d currency %q does not match run currency %q", ErrInvalidStatementRecord, i+1, rec.Currency, runCurrency)
		}
		status := strings.ToLower(strings.TrimSpace(rec.Status))
		if status != "completed" && status != "failed" && status != "pending" {
			return nil, MatchSummary{}, fmt.Errorf("%w: row %d invalid status %q", ErrInvalidStatementRecord, i+1, rec.Status)
		}

		normalizedBank = append(normalizedBank, BankStatementRecord{
			ExternalRef: ref,
			Direction:   dir,
			Amount:      rec.Amount,
			Currency:    curr,
			Status:      status,
		})
	}

	internalByRef := make(map[string]InternalPaymentRecord, len(internalPayments))
	for _, p := range internalPayments {
		internalByRef[p.ExternalRef] = p
	}
	seenInternal := make(map[string]struct{}, len(internalPayments))

	items := make([]MatchedItem, 0, len(normalizedBank)+len(internalPayments))
	var matchedCount, discrepancyCount int32

	for _, b := range normalizedBank {
		bankAmt := b.Amount
		p, ok := internalByRef[b.ExternalRef]
		if !ok {
			discrepancyCount++
			items = append(items, MatchedItem{
				ExternalRef:      b.ExternalRef,
				MatchStatus:      "missing_in_fincore",
				BankAmount:       &bankAmt,
				BankDirection:    b.Direction,
				BankStatus:       b.Status,
				Currency:         runCurrency,
				ResolutionStatus: "open",
			})
			continue
		}

		seenInternal[b.ExternalRef] = struct{}{}
		internalAmt := p.Amount

		matchStatus := "matched"
		resolutionStatus := "not_applicable"
		switch {
		case b.Amount != p.Amount:
			matchStatus = "amount_mismatch"
			resolutionStatus = "open"
		case b.Direction != p.Direction:
			matchStatus = "direction_mismatch"
			resolutionStatus = "open"
		case b.Status != p.Status:
			matchStatus = "status_mismatch"
			resolutionStatus = "open"
		}

		if matchStatus == "matched" {
			matchedCount++
		} else {
			discrepancyCount++
		}

		items = append(items, MatchedItem{
			ExternalRef:       b.ExternalRef,
			PaymentID:         p.PaymentID,
			MatchStatus:       matchStatus,
			BankAmount:        &bankAmt,
			InternalAmount:    &internalAmt,
			BankDirection:     b.Direction,
			InternalDirection: p.Direction,
			BankStatus:        b.Status,
			InternalStatus:    p.Status,
			Currency:          runCurrency,
			ResolutionStatus:  resolutionStatus,
		})
	}

	for _, p := range internalPayments {
		if _, seen := seenInternal[p.ExternalRef]; seen {
			continue
		}
		internalAmt := p.Amount
		discrepancyCount++
		items = append(items, MatchedItem{
			ExternalRef:       p.ExternalRef,
			PaymentID:         p.PaymentID,
			MatchStatus:       "missing_in_bank",
			InternalAmount:    &internalAmt,
			InternalDirection: p.Direction,
			InternalStatus:    p.Status,
			Currency:          runCurrency,
			ResolutionStatus:  "open",
		})
	}

	runStatus := "completed"
	if discrepancyCount > 0 {
		runStatus = "has_discrepancies"
	}

	summary := MatchSummary{
		TotalBankRecords:     int32(len(normalizedBank)),
		TotalInternalRecords: int32(len(internalPayments)),
		MatchedCount:         matchedCount,
		DiscrepancyCount:     discrepancyCount,
		Status:               runStatus,
	}
	return items, summary, nil
}
