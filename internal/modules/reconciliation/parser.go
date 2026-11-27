package reconciliation

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var expectedCSVHeader = []string{"external_ref", "direction", "amount", "currency", "status"}

// ParseStatementCSV parses a bank statement CSV stream with required header:
// external_ref,direction,amount,currency,status.
// Amounts are parsed strictly as positive int64 minor units.
func ParseStatementCSV(r io.Reader) ([]BankStatementRecord, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read header: %v", ErrInvalidCSVFormat, err)
	}
	if len(header) != len(expectedCSVHeader) {
		return nil, fmt.Errorf("%w: expected columns %v", ErrInvalidCSVFormat, expectedCSVHeader)
	}
	for i, col := range expectedCSVHeader {
		actual := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header[i], "\ufeff")))
		if actual != col {
			return nil, fmt.Errorf("%w: column %d must be %q, got %q", ErrInvalidCSVFormat, i+1, col, header[i])
		}
	}

	records := make([]BankStatementRecord, 0)
	rowNum := 1
	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: row %d: %v", ErrInvalidCSVFormat, rowNum+1, err)
		}
		rowNum++

		if len(row) != len(expectedCSVHeader) {
			return nil, fmt.Errorf("%w: row %d has %d columns, expected %d", ErrInvalidCSVFormat, rowNum, len(row), len(expectedCSVHeader))
		}

		rawAmt := strings.TrimSpace(row[2])
		amt, err := strconv.ParseInt(rawAmt, 10, 64)
		if err != nil || amt <= 0 {
			return nil, fmt.Errorf("%w: row %d amount %q must be a positive integer in minor units", ErrInvalidStatementRecord, rowNum, rawAmt)
		}

		records = append(records, BankStatementRecord{
			ExternalRef: strings.TrimSpace(row[0]),
			Direction:   strings.ToLower(strings.TrimSpace(row[1])),
			Amount:      amt,
			Currency:    strings.ToUpper(strings.TrimSpace(row[3])),
			Status:      strings.ToLower(strings.TrimSpace(row[4])),
		})
	}

	return records, nil
}
