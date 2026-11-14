package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// UserRepository persists users.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetByPhone(ctx context.Context, phone string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	Update(ctx context.Context, u *User) error
	UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error
	SetEmailVerified(ctx context.Context, id uuid.UUID, email string) error
	SetBlocked(ctx context.Context, id uuid.UUID, blocked bool) error
	SetRole(ctx context.Context, id uuid.UUID, role Role) error
	Delete(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context, p Pagination, query string) (Page[User], error)
	Count(ctx context.Context) (int64, error)
	CountBlocked(ctx context.Context) (int64, error)
}

// ContactRepository persists a user's private phone book.
type ContactRepository interface {
	Create(ctx context.Context, c *Contact) error
	GetByID(ctx context.Context, id uuid.UUID) (*Contact, error)
	List(ctx context.Context, ownerID uuid.UUID, q string, favorite *bool, p Pagination) (Page[Contact], error)
	Update(ctx context.Context, c *Contact) error
	Delete(ctx context.Context, id, ownerID uuid.UUID) error
	// BulkUpsert returns the IDs of the stored rows (existing rows keep their ID).
	BulkUpsert(ctx context.Context, ownerID uuid.UUID, items []Contact) ([]uuid.UUID, error)
	Count(ctx context.Context) (int64, error)
	StatsByOwner(ctx context.Context, ownerID uuid.UUID) (ContactStats, error)
}

// NumberRepository persists caller-ID profiles, tags, names and spam reports.
type NumberRepository interface {
	Get(ctx context.Context, phone string) (*Number, error)
	Upsert(ctx context.Context, n *Number) error
	IncrementLookups(ctx context.Context, phone string) error
	UpdateDetails(ctx context.Context, phone string, name, carrier, country *string) (*Number, error)
	ListTags(ctx context.Context, phone string) ([]Tag, error)
	AddTag(ctx context.Context, t *Tag) error
	GetTag(ctx context.Context, id uuid.UUID) (*Tag, error)
	DeleteTag(ctx context.Context, id uuid.UUID) error
	AddName(ctx context.Context, n *NumberName) error
	GetName(ctx context.Context, id uuid.UUID) (*NumberName, error)
	DeleteName(ctx context.Context, id uuid.UUID) error
	ListNames(ctx context.Context, phone string) ([]NumberName, error)
	ListNameCounts(ctx context.Context, phone string) ([]NameCount, error)
	RecomputeDisplayName(ctx context.Context, phone string) error
	AddSpamReport(ctx context.Context, r *SpamReport) error
	GetSpamReport(ctx context.Context, id uuid.UUID) (*SpamReport, error)
	DeleteSpamReport(ctx context.Context, id uuid.UUID) error
	ListReportsForPhone(ctx context.Context, phone string, limit int) ([]SpamReport, error)
	SpamSummary(ctx context.Context, phone string) (*SpamSummary, error)
	RecomputeSpamScore(ctx context.Context, phone string) error
	TopSpam(ctx context.Context, limit int) ([]Number, error)
	TopTrending(ctx context.Context, limit int) ([]Number, error)
	Search(ctx context.Context, q string, limit int) ([]Number, error)
	Count(ctx context.Context) (int64, error)
	TotalLookups(ctx context.Context) (int64, error)
	CountReports(ctx context.Context) (int64, error)
	CountReportsByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountTags(ctx context.Context) (int64, error)
	CountTagsByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountNamesByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	TopTags(ctx context.Context, limit int) ([]TagCount, error)
	ListReports(ctx context.Context, p Pagination) (Page[SpamReport], error)
}

// TokenRepository stores hashed refresh tokens for rotation and revocation.
type TokenRepository interface {
	Create(ctx context.Context, t *RefreshToken) error
	GetByHash(ctx context.Context, hash string) (*RefreshToken, error)
	// ConsumeForRotation atomically revokes a refresh token and returns it.
	// Only the first concurrent rotation can win, which prevents replay.
	ConsumeForRotation(ctx context.Context, hash string) (*RefreshToken, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
	RevokeForUser(ctx context.Context, id, userID uuid.UUID) error
	ListActiveForUser(ctx context.Context, userID uuid.UUID) ([]RefreshToken, error)
	CountActiveForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// AuditRepository reads the worker-side audit trail for the admin area.
type AuditRepository interface {
	List(ctx context.Context, f AuditFilter) (Page[AuditEvent], error)
}

// EventRepository is the worker-side idempotent audit store.
type EventRepository interface {
	Insert(ctx context.Context, topic string, ev Event) (bool, error)
}

// EmailRepository persists one-time email codes and the transactional outbox.
type EmailRepository interface {
	// CreateVerification stores a new code and, atomically, the Kafka event
	// that will ask the worker to deliver it.
	CreateVerification(ctx context.Context, v *EmailVerification, outbox *OutboxMessage) error
	GetVerification(ctx context.Context, id uuid.UUID) (*EmailVerification, error)
	// LatestVerification returns the newest unconsumed, unexpired code for an
	// address and purpose.
	LatestVerification(ctx context.Context, email, purpose string) (*EmailVerification, error)
	// LastRequestedAt returns the most recent code request for an address and
	// purpose, or the zero time when none exists. It enforces the cooldown.
	LastRequestedAt(ctx context.Context, email, purpose string) (time.Time, error)
	// IncrementAttempts records a wrong guess and returns the new attempt count.
	IncrementAttempts(ctx context.Context, id uuid.UUID) (int, error)
	ConsumeVerification(ctx context.Context, id uuid.UUID) (bool, error)
	MarkDelivered(ctx context.Context, id uuid.UUID) error
	MarkDeliveryFailed(ctx context.Context, id uuid.UUID, reason string) error
	// SetEmailVerified binds a verified address to a user and marks it verified.
	SetEmailVerified(ctx context.Context, userID uuid.UUID, email string) error

	// Enqueue stores a standalone outbox event (used by the delivery worker).
	Enqueue(ctx context.Context, msg *OutboxMessage) error
	ClaimOutbox(ctx context.Context, limit, maxAttempts int) ([]OutboxMessage, error)
	MarkOutboxPublished(ctx context.Context, eventID uuid.UUID) error
	MarkOutboxFailed(ctx context.Context, eventID uuid.UUID, reason string) error
	// OutboxBacklog counts messages that can still be published; rows that
	// exhausted their retry budget are excluded.
	OutboxBacklog(ctx context.Context, maxAttempts int) (int64, error)
	// AcquireOutboxLock prevents two relay instances from double-publishing.
	// It returns a release function and false when another relay holds the lock.
	AcquireOutboxLock(ctx context.Context) (release func(), acquired bool, err error)
	PurgeExpired(ctx context.Context, before time.Time) (int64, error)
}
