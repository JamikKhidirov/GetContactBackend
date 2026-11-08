package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EmailRepo is the pgx implementation of domain.EmailRepository.
type EmailRepo struct{ db *pgxpool.Pool }

// NewEmailRepo builds the email verification/outbox repository.
func NewEmailRepo(db *pgxpool.Pool) *EmailRepo { return &EmailRepo{db: db} }

const emailVerificationColumns = `id, email, user_id, purpose, code_hash, code_ciphertext,
	attempts, max_attempts, delivery_attempts, COALESCE(request_ip, ''), COALESCE(user_agent, ''),
	expires_at, consumed_at, delivered_at, COALESCE(delivery_error, ''), last_attempt_at, created_at`

func scanEmailVerification(row pgx.Row) (*domain.EmailVerification, error) {
	var v domain.EmailVerification
	if err := row.Scan(&v.ID, &v.Email, &v.UserID, &v.Purpose, &v.CodeHash, &v.CodeCiphertext,
		&v.Attempts, &v.MaxAttempts, &v.DeliveryAttempts, &v.RequestIP, &v.UserAgent,
		&v.ExpiresAt, &v.ConsumedAt, &v.DeliveredAt, &v.DeliveryError, &v.LastAttemptAt, &v.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &v, nil
}

// CreateVerification writes the code and its delivery event in one transaction.
// Either both rows commit or neither does, which is what makes the outbox
// reliable: a committed code always has a pending Kafka event.
func (r *EmailRepo) CreateVerification(ctx context.Context, v *domain.EmailVerification, outbox *domain.OutboxMessage) error {
	defer observe("email.create_verification", time.Now())
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `INSERT INTO email_verifications
			(id, email, user_id, purpose, code_hash, code_ciphertext, attempts, max_attempts,
			 request_ip, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 0, $7, NULLIF($8, ''), NULLIF($9, ''), $10)
		RETURNING created_at`,
		v.ID, v.Email, v.UserID, v.Purpose, v.CodeHash, v.CodeCiphertext, v.MaxAttempts,
		v.RequestIP, v.UserAgent, v.ExpiresAt,
	).Scan(&v.CreatedAt); err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, outbox); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetVerification loads one code by id (used by the delivery worker).
func (r *EmailRepo) GetVerification(ctx context.Context, id uuid.UUID) (*domain.EmailVerification, error) {
	defer observe("email.get_verification", time.Now())
	return scanEmailVerification(r.db.QueryRow(ctx, `SELECT `+emailVerificationColumns+`
		FROM email_verifications WHERE id = $1`, id))
}

// LatestVerification returns the newest usable code for an address and purpose.
func (r *EmailRepo) LatestVerification(ctx context.Context, email, purpose string) (*domain.EmailVerification, error) {
	defer observe("email.latest_verification", time.Now())
	return scanEmailVerification(r.db.QueryRow(ctx, `SELECT `+emailVerificationColumns+`
		FROM email_verifications
		WHERE LOWER(email) = LOWER($1) AND purpose = $2
		  AND consumed_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC LIMIT 1`, email, purpose))
}

// LastRequestedAt supports the per-address cooldown and anti-spam policy.
func (r *EmailRepo) LastRequestedAt(ctx context.Context, email, purpose string) (time.Time, error) {
	defer observe("email.last_requested_at", time.Now())
	var t time.Time
	err := r.db.QueryRow(ctx, `SELECT created_at FROM email_verifications
		WHERE LOWER(email) = LOWER($1) AND purpose = $2
		ORDER BY created_at DESC LIMIT 1`, email, purpose).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return t, err
}

// IncrementAttempts records a wrong guess and returns the new count.
func (r *EmailRepo) IncrementAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	defer observe("email.increment_attempts", time.Now())
	var attempts int
	err := r.db.QueryRow(ctx, `UPDATE email_verifications
		SET attempts = attempts + 1, last_attempt_at = NOW()
		WHERE id = $1 RETURNING attempts`, id).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	return attempts, err
}

// ConsumeVerification atomically burns a code. Only the first concurrent
// confirmation can win, which prevents replay.
func (r *EmailRepo) ConsumeVerification(ctx context.Context, id uuid.UUID) (bool, error) {
	defer observe("email.consume_verification", time.Now())
	tag, err := r.db.Exec(ctx, `UPDATE email_verifications
		SET consumed_at = NOW()
		WHERE id = $1 AND consumed_at IS NULL AND expires_at > NOW()`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MarkDelivered records a successful provider hand-off.
func (r *EmailRepo) MarkDelivered(ctx context.Context, id uuid.UUID) error {
	defer observe("email.mark_delivered", time.Now())
	_, err := r.db.Exec(ctx, `UPDATE email_verifications
		SET delivered_at = NOW(), delivery_error = NULL, last_attempt_at = NOW()
		WHERE id = $1 AND delivered_at IS NULL`, id)
	return err
}

// MarkDeliveryFailed records a delivery error for observability and retries.
func (r *EmailRepo) MarkDeliveryFailed(ctx context.Context, id uuid.UUID, reason string) error {
	defer observe("email.mark_delivery_failed", time.Now())
	_, err := r.db.Exec(ctx, `UPDATE email_verifications
		SET delivery_attempts = delivery_attempts + 1,
		    delivery_error = $2, last_attempt_at = NOW()
		WHERE id = $1`, id, truncateString(reason, 1000))
	return err
}

// SetEmailVerified binds an address to a user after proof of ownership.
func (r *EmailRepo) SetEmailVerified(ctx context.Context, userID uuid.UUID, email string) error {
	defer observe("email.set_email_verified", time.Now())
	tag, err := r.db.Exec(ctx, `UPDATE users
		SET email = $2, email_verified = TRUE, email_verified_at = NOW()
		WHERE id = $1`, userID, email)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Enqueue stores a standalone event for the relay (delivery receipts).
func (r *EmailRepo) Enqueue(ctx context.Context, msg *domain.OutboxMessage) error {
	defer observe("email.enqueue", time.Now())
	return insertOutbox(ctx, r.db, msg)
}

// outboxExecer is implemented by both pgxpool.Pool and pgx.Tx.
type outboxExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertOutbox(ctx context.Context, q outboxExecer, msg *domain.OutboxMessage) error {
	if msg == nil {
		return errors.New("outbox message is required")
	}
	payload := msg.Payload
	if !json.Valid(payload) {
		return errors.New("outbox payload is not valid JSON")
	}
	_, err := q.Exec(ctx, `INSERT INTO email_outbox
			(event_id, event_type, event_version, aggregate_id, payload, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_id) DO NOTHING`,
		msg.EventID, msg.EventType, msg.EventVersion, msg.AggregateID, string(payload), msg.OccurredAt)
	return err
}

// ClaimOutbox reserves a batch of pending events. The attempts counter is
// incremented atomically; SKIP LOCKED keeps multiple relays (or a relay racing
// a cleanup) from stepping on each other.
func (r *EmailRepo) ClaimOutbox(ctx context.Context, limit, maxAttempts int) ([]domain.OutboxMessage, error) {
	defer observe("email.claim_outbox", time.Now())
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.Query(ctx, `WITH claimed AS (
			SELECT event_id FROM email_outbox
			WHERE published_at IS NULL AND attempts < $2
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE email_outbox o
		SET attempts = o.attempts + 1
		FROM claimed c
		WHERE o.event_id = c.event_id
		RETURNING o.event_id, o.event_type, o.event_version, o.aggregate_id, o.payload,
		          o.occurred_at, o.created_at, o.published_at, o.attempts, COALESCE(o.last_error, '')`,
		limit, maxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OutboxMessage, 0, limit)
	for rows.Next() {
		var m domain.OutboxMessage
		var payload []byte
		if err := rows.Scan(&m.EventID, &m.EventType, &m.EventVersion, &m.AggregateID, &payload,
			&m.OccurredAt, &m.CreatedAt, &m.PublishedAt, &m.Attempts, &m.LastError); err != nil {
			return nil, err
		}
		m.Payload = payload
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkOutboxPublished acknowledges a successfully published event.
func (r *EmailRepo) MarkOutboxPublished(ctx context.Context, eventID uuid.UUID) error {
	defer observe("email.mark_outbox_published", time.Now())
	_, err := r.db.Exec(ctx, `UPDATE email_outbox
		SET published_at = NOW(), last_error = NULL WHERE event_id = $1`, eventID)
	return err
}

// MarkOutboxFailed stores the last publish error for operators.
func (r *EmailRepo) MarkOutboxFailed(ctx context.Context, eventID uuid.UUID, reason string) error {
	defer observe("email.mark_outbox_failed", time.Now())
	_, err := r.db.Exec(ctx, `UPDATE email_outbox
		SET last_error = $2 WHERE event_id = $1`, eventID, truncateString(reason, 1000))
	return err
}

// OutboxBacklog reports how many events still need publishing.
func (r *EmailRepo) OutboxBacklog(ctx context.Context) (int64, error) {
	defer observe("email.outbox_backlog", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM email_outbox WHERE published_at IS NULL`).Scan(&n)
	return n, err
}

const outboxRelayLockKey int64 = 0x67636f7574626f78

// AcquireOutboxLock elects a single relay using a session-level advisory lock.
// The lock is released explicitly or automatically when the connection closes.
func (r *EmailRepo) AcquireOutboxLock(ctx context.Context) (func(), bool, error) {
	conn, err := r.db.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, outboxRelayLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !acquired {
		conn.Release()
		return func() {}, false, nil
	}
	release := func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, outboxRelayLockKey)
		conn.Release()
	}
	return release, true, nil
}

// PurgeExpired deletes old consumed/expired codes and published outbox rows.
func (r *EmailRepo) PurgeExpired(ctx context.Context, before time.Time) (int64, error) {
	defer observe("email.purge_expired", time.Now())
	tag, err := r.db.Exec(ctx, `DELETE FROM email_verifications
		WHERE created_at < $1 AND (consumed_at IS NOT NULL OR expires_at < NOW())`, before)
	if err != nil {
		return 0, err
	}
	removed := tag.RowsAffected()
	tag, err = r.db.Exec(ctx, `DELETE FROM email_outbox
		WHERE created_at < $1 AND published_at IS NOT NULL`, before)
	if err != nil {
		return removed, err
	}
	return removed + tag.RowsAffected(), nil
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Ensure interface conformance at compile time.
var _ domain.EmailRepository = (*EmailRepo)(nil)
