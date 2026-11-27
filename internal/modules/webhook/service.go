package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	webhookdb "github.com/BaoTrung37/FinCore/internal/modules/webhook/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultMaxAttempts int32 = 5

var (
	ErrWebhookNotFound = errors.New("webhook endpoint not found")
	ErrInvalidURL      = errors.New("webhook url must be a valid http or https URL")
)

// Event represents a domain event to be persisted in the transactional outbox.
type Event struct {
	UserID        string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       any
}

// Service manages webhook endpoints and transactional outbox event publishing.
type Service struct {
	q webhookdb.Querier
}

// NewService constructs a webhook Service backed by a PostgreSQL pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: webhookdb.New(pool)}
}

// NewServiceWithQuerier constructs a webhook Service with a custom Querier (useful for unit tests).
func NewServiceWithQuerier(q webhookdb.Querier) *Service {
	return &Service{q: q}
}

// PublishTx inserts an outbox event inside the caller's database transaction.
func (s *Service) PublishTx(ctx context.Context, tx pgx.Tx, evt Event) error {
	var rawPayload []byte
	switch p := evt.Payload.(type) {
	case json.RawMessage:
		rawPayload = p
	case []byte:
		rawPayload = p
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("marshal outbox payload: %w", err)
		}
		rawPayload = b
	}

	_, err := webhookdb.New(tx).InsertOutboxEvent(ctx, webhookdb.InsertOutboxEventParams{
		UserID:        evt.UserID,
		AggregateType: evt.AggregateType,
		AggregateID:   evt.AggregateID,
		EventType:     evt.EventType,
		Payload:       rawPayload,
		MaxAttempts:   defaultMaxAttempts,
	})
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// CreateEndpoint registers a new active webhook endpoint with a generated HMAC secret.
func (s *Service) CreateEndpoint(ctx context.Context, userID, rawURL string, events []string) (webhookdb.WebhookEndpoint, error) {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return webhookdb.WebhookEndpoint{}, ErrInvalidURL
	}

	secret, err := generateSecret()
	if err != nil {
		return webhookdb.WebhookEndpoint{}, fmt.Errorf("generate webhook secret: %w", err)
	}

	if events == nil {
		events = []string{}
	}

	return s.q.CreateWebhookEndpoint(ctx, webhookdb.CreateWebhookEndpointParams{
		UserID: userID,
		Url:    rawURL,
		Secret: secret,
		Events: events,
	})
}

// ListEndpoints returns all active webhook endpoints owned by the user.
func (s *Service) ListEndpoints(ctx context.Context, userID string) ([]webhookdb.WebhookEndpoint, error) {
	items, err := s.q.ListWebhookEndpointsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return []webhookdb.WebhookEndpoint{}, nil
	}
	return items, nil
}

// DeleteEndpoint deactivates a webhook endpoint owned by the user.
func (s *Service) DeleteEndpoint(ctx context.Context, userID, endpointID string) error {
	rows, err := s.q.DeactivateWebhookEndpoint(ctx, webhookdb.DeactivateWebhookEndpointParams{
		ID:     endpointID,
		UserID: userID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrWebhookNotFound
	}
	return nil
}

// ComputeSignature returns the hex-encoded HMAC-SHA256 signature for an outbound webhook payload.
func ComputeSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks whether signature matches the HMAC-SHA256 digest of payload under secret.
func VerifySignature(secret string, payload []byte, signature string) bool {
	if secret == "" || signature == "" {
		return false
	}
	expected := ComputeSignature(secret, payload)
	return hmac.Equal([]byte(expected), []byte(signature))
}

func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}
