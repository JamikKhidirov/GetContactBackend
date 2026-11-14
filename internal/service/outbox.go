package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
)

// OutboxRelay publishes transactional outbox rows to Kafka. The business write
// and the event commit atomically in PostgreSQL; the relay closes the final gap
// between "committed" and "published", retrying until Kafka acknowledges.
type OutboxRelay struct {
	repo        domain.EmailRepository
	pub         events.Publisher
	log         *slog.Logger
	interval    time.Duration
	batch       int
	maxAttempts int
	lastPurge   time.Time
}

// NewOutboxRelay builds the relay loop.
func NewOutboxRelay(repo domain.EmailRepository, pub events.Publisher, log *slog.Logger, interval time.Duration, batch, maxAttempts int) *OutboxRelay {
	if interval <= 0 {
		interval = time.Second
	}
	if batch <= 0 {
		batch = 50
	}
	if maxAttempts <= 0 {
		maxAttempts = 25
	}
	return &OutboxRelay{repo: repo, pub: pub, log: log, interval: interval, batch: batch, maxAttempts: maxAttempts}
}

// Run blocks until the context is cancelled.
func (r *OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	r.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce publishes at most one batch and updates the backlog gauge.
func (r *OutboxRelay) RunOnce(ctx context.Context) {
	release, acquired, err := r.repo.AcquireOutboxLock(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.log.Warn("outbox relay lock failed", "error", err)
		}
		return
	}
	if !acquired {
		r.refreshBacklog(ctx)
		return
	}
	defer release()

	msgs, err := r.repo.ClaimOutbox(ctx, r.batch, r.maxAttempts)
	if err != nil {
		if ctx.Err() == nil {
			r.log.Warn("outbox claim failed", "error", err)
		}
		return
	}
	for i := range msgs {
		m := msgs[i]
		ev := domain.Event{
			ID:          m.EventID,
			Type:        m.EventType,
			Version:     m.EventVersion,
			OccurredAt:  m.OccurredAt,
			AggregateID: m.AggregateID,
			Payload:     json.RawMessage(m.Payload),
		}
		pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := r.pub.Publish(pubCtx, ev)
		cancel()
		if err != nil {
			r.log.Warn("outbox publish failed", "event_id", m.EventID, "type", m.EventType, "attempt", m.Attempts, "error", err)
			if markErr := r.repo.MarkOutboxFailed(ctx, m.EventID, err.Error()); markErr != nil && ctx.Err() == nil {
				r.log.Warn("outbox failure not recorded", "event_id", m.EventID, "error", markErr)
			}
			continue
		}
		if err := r.repo.MarkOutboxPublished(ctx, m.EventID); err != nil && ctx.Err() == nil {
			// The event reached Kafka; at worst it is published again later.
			r.log.Warn("outbox publish not acknowledged", "event_id", m.EventID, "error", err)
		}
	}
	if len(msgs) > 0 {
		r.log.Debug("outbox batch published", "count", len(msgs))
	}
	r.refreshBacklog(ctx)

	if time.Since(r.lastPurge) > time.Hour {
		r.lastPurge = time.Now()
		if n, err := r.repo.PurgeExpired(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
			if ctx.Err() == nil {
				r.log.Warn("email retention purge failed", "error", err)
			}
		} else if n > 0 {
			r.log.Info("email retention purge removed rows", "count", n)
		}
	}
}

func (r *OutboxRelay) refreshBacklog(ctx context.Context) {
	n, err := r.repo.OutboxBacklog(ctx, r.maxAttempts)
	if err != nil {
		return
	}
	metrics.EmailOutboxBacklog.Set(float64(n))
}
