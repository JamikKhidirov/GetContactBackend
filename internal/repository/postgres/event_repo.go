package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EventRepo is the worker-side idempotent audit/event store.
type EventRepo struct{ db *pgxpool.Pool }

// NewEventRepo builds an event repository.
func NewEventRepo(db *pgxpool.Pool) *EventRepo { return &EventRepo{db: db} }

// Insert stores an event exactly once. It returns false for a duplicate delivery.
func (r *EventRepo) Insert(ctx context.Context, topic string, ev domain.Event) (bool, error) {
	defer observe("events.insert", time.Now())
	payload, err := json.Marshal(ev)
	if err != nil {
		return false, err
	}
	tag, err := r.db.Exec(ctx, `INSERT INTO audit_events
		(id, topic, event_type, aggregate_id, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		ev.ID, topic, ev.Type, ev.AggregateID, payload, ev.OccurredAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

var _ domain.EventRepository = (*EventRepo)(nil)
