package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	webhookdb "github.com/BaoTrung37/FinCore/internal/modules/webhook/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultBatchSize   int32         = 50
	defaultBaseBackoff time.Duration = 10 * time.Second
	maxBackoff         time.Duration = time.Hour
)

// RelayWorker polls pending outbox_events and delivers signed HTTP POST webhooks.
type RelayWorker struct {
	pool        *pgxpool.Pool
	log         *slog.Logger
	httpClient  *http.Client
	interval    time.Duration
	batchSize   int32
	baseBackoff time.Duration
}

// NewRelayWorker constructs a RelayWorker.
func NewRelayWorker(pool *pgxpool.Pool, log *slog.Logger, interval time.Duration) *RelayWorker {
	return &RelayWorker{
		pool: pool,
		log:  log,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		interval:    interval,
		batchSize:   defaultBatchSize,
		baseBackoff: defaultBaseBackoff,
	}
}

// Start runs the polling loop until ctx is canceled.
func (w *RelayWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.ProcessBatch(ctx); err != nil && ctx.Err() == nil {
				w.log.Error("outbox relay batch failed", slog.Any("error", err))
			}
		}
	}
}

// ProcessBatch claims pending outbox events with FOR UPDATE SKIP LOCKED and delivers them.
// TODO(m5-hardening): Decouple outbound HTTP delivery from the DB transaction
// (e.g., claim with status='processing' + lease timeout, deliver outside tx, then mark completed/retry/failed)
// so slow webhook endpoints do not hold DB connections and row locks open.
func (w *RelayWorker) ProcessBatch(ctx context.Context) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin outbox tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := webhookdb.New(tx)
	events, err := q.ClaimPendingOutboxEvents(ctx, w.batchSize)
	if err != nil {
		return fmt.Errorf("claim pending outbox events: %w", err)
	}

	for _, evt := range events {
		if err := w.deliverEvent(ctx, q, evt); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (w *RelayWorker) deliverEvent(ctx context.Context, q webhookdb.Querier, evt webhookdb.OutboxEvent) error {
	endpoints, err := q.ListActiveWebhooksForEvent(ctx, webhookdb.ListActiveWebhooksForEventParams{
		UserID:  evt.UserID,
		Column2: evt.EventType,
	})
	if err != nil {
		return fmt.Errorf("list active webhooks: %w", err)
	}

	if len(endpoints) == 0 {
		return q.MarkOutboxEventCompleted(ctx, evt.ID)
	}

	var deliveryErr error
	for _, ep := range endpoints {
		if err := w.sendHTTP(ctx, ep, evt); err != nil {
			deliveryErr = err
			break
		}
	}

	if deliveryErr == nil {
		return q.MarkOutboxEventCompleted(ctx, evt.ID)
	}

	nextAttempts := evt.Attempts + 1
	if nextAttempts >= evt.MaxAttempts {
		return q.MarkOutboxEventFailed(ctx, webhookdb.MarkOutboxEventFailedParams{
			ID:        evt.ID,
			LastError: deliveryErr.Error(),
		})
	}

	nextRetry := time.Now().UTC().Add(BackoffDuration(evt.Attempts, w.baseBackoff))
	return q.MarkOutboxEventRetry(ctx, webhookdb.MarkOutboxEventRetryParams{
		ID:          evt.ID,
		NextRetryAt: nextRetry,
		LastError:   deliveryErr.Error(),
	})
}

func (w *RelayWorker) sendHTTP(ctx context.Context, ep webhookdb.WebhookEndpoint, evt webhookdb.OutboxEvent) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Url, bytes.NewReader(evt.Payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-FinCore-Event", evt.EventType)
	req.Header.Set("X-FinCore-Delivery", evt.ID)
	req.Header.Set("X-FinCore-Signature", ComputeSignature(ep.Secret, evt.Payload))

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook %s: %w", ep.Url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook %s returned status %d", ep.Url, resp.StatusCode)
	}
	return nil
}

// BackoffDuration computes capped exponential backoff: base * 2^attempt (capped at 1h).
func BackoffDuration(attempt int32, base time.Duration) time.Duration {
	if attempt <= 0 {
		return base
	}
	shift := min(attempt, 10)
	d := base * time.Duration(1<<shift)
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}
