package payment

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// BankRequest represents an external deposit or withdrawal instruction sent to a bank gateway.
type BankRequest struct {
	Direction         string // "deposit" or "withdrawal"
	AccountID         string
	Amount            int64
	Currency          string
	BankCode          string
	BankAccountNumber string
	IdempotencyKey    string
}

// BankResponse represents the synchronous result from a bank gateway.
type BankResponse struct {
	ExternalRef   string
	Status        string // "completed", "pending", or "failed"
	FailureReason string
}

// BankGateway abstracts external bank rails for deposits and withdrawals.
type BankGateway interface {
	InitiateDeposit(ctx context.Context, req BankRequest) (BankResponse, error)
	InitiateWithdrawal(ctx context.Context, req BankRequest) (BankResponse, error)
}

// MockBankGateway is a deterministic bank adapter for local development and testing.
//
// Behavior rules:
//   - BankCode == "MOCK_FAIL" or BankAccountNumber == "0000000000" -> returns status "failed"
//   - BankCode == "MOCK_PENDING" -> returns status "pending" (settle via callback endpoint)
//   - Otherwise -> returns status "completed"
type MockBankGateway struct{}

// NewMockBankGateway constructs a MockBankGateway.
func NewMockBankGateway() *MockBankGateway {
	return &MockBankGateway{}
}

func (g *MockBankGateway) InitiateDeposit(_ context.Context, req BankRequest) (BankResponse, error) {
	return g.simulate(req), nil
}

func (g *MockBankGateway) InitiateWithdrawal(_ context.Context, req BankRequest) (BankResponse, error) {
	return g.simulate(req), nil
}

func (g *MockBankGateway) simulate(req BankRequest) BankResponse {
	extRef := generateExternalRef(req.Direction)
	code := strings.ToUpper(strings.TrimSpace(req.BankCode))
	accNum := strings.TrimSpace(req.BankAccountNumber)

	if code == "MOCK_FAIL" || accNum == "0000000000" {
		return BankResponse{
			ExternalRef:   extRef,
			Status:        "failed",
			FailureReason: "mock bank rejected transaction",
		}
	}
	if code == "MOCK_PENDING" {
		return BankResponse{
			ExternalRef: extRef,
			Status:      "pending",
		}
	}
	return BankResponse{
		ExternalRef: extRef,
		Status:      "completed",
	}
}

func generateExternalRef(direction string) string {
	prefix := "MDEP"
	if direction == "withdrawal" {
		prefix = "MWDR"
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-FALLBACK", prefix)
	}
	return fmt.Sprintf("%s-%s", prefix, strings.ToUpper(hex.EncodeToString(buf)))
}

// ComputeCallbackSignature computes an HMAC-SHA256 hex signature over the raw callback payload.
func ComputeCallbackSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyCallbackSignature performs a constant-time HMAC-SHA256 verification.
func VerifyCallbackSignature(secret string, payload []byte, signature string) bool {
	if secret == "" {
		return true
	}
	expected := ComputeCallbackSignature(secret, payload)
	return hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(signature))), []byte(expected))
}
