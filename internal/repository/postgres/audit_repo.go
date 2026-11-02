package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditRepo reads the worker-side audit trail for the admin area.
type AuditRepo struct{ db *pgxpool.Pool }

// NewAuditRepo builds an audit repository.
func NewAuditRepo(db *pgxpool.Pool) *AuditRepo { return &AuditRepo{db: db} }

// List returns a filtered page of audit events, newest first.
func (r *AuditRepo) List(ctx context.Context, f domain.AuditFilter) (domain.Page[domain.AuditEvent], error) {
	defer observe("audit.list", time.Now())
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = 20
	}
	if f.PerPage > 100 {
		f.PerPage = 100
	}
	page := domain.Page[domain.AuditEvent]{Page: f.Page, PerPage: f.PerPage}
	const where = `WHERE ($1 = '' OR event_type = $1) AND ($2 = '' OR aggregate_id = $2)`

	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events `+where,
		f.EventType, f.AggregateID).Scan(&page.Total); err != nil {
		return page, err
	}

	rows, err := r.db.Query(ctx, `SELECT id, topic, event_type, aggregate_id, payload, occurred_at, consumed_at
		FROM audit_events `+where+` ORDER BY consumed_at DESC LIMIT $3 OFFSET $4`,
		f.EventType, f.AggregateID, f.PerPage, (f.Page-1)*f.PerPage)
	if err != nil {
		return page, err
	}
	defer rows.Close()

	page.Items = make([]domain.AuditEvent, 0, f.PerPage)
	for rows.Next() {
		var (
			ev      domain.AuditEvent
			payload []byte
		)
		if err := rows.Scan(&ev.ID, &ev.Topic, &ev.Type, &ev.AggregateID,
			&payload, &ev.OccurredAt, &ev.ConsumedAt); err != nil {
			return page, err
		}
		ev.Payload = json.RawMessage(payload)
		page.Items = append(page.Items, ev)
	}
	return page, rows.Err()
}

var _ domain.AuditRepository = (*AuditRepo)(nil)
