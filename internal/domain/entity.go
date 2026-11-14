// Package domain contains the business entities, invariants and repository ports.
// It has no dependency on transport, database or framework packages.
package domain

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Role is an authorization role attached to a user.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User is a registered platform member.
type User struct {
	ID              uuid.UUID  `json:"id"`
	Phone           string     `json:"phone"`
	Email           string     `json:"email,omitempty"`
	EmailVerified   bool       `json:"email_verified"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	PasswordHash    string     `json:"-"`
	// TokenVersion is embedded in access tokens. Bumping it invalidates every
	// outstanding token for the account (block, password change, role change).
	TokenVersion int       `json:"-"`
	DisplayName  string    `json:"display_name"`
	Role         Role      `json:"role"`
	IsBlocked    bool      `json:"is_blocked"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Public returns a copy safe to expose over the API.
func (u User) Public() User {
	u.PasswordHash = ""
	return u
}

// Contact is a personal phone book entry owned by a user.
type Contact struct {
	ID         uuid.UUID `json:"id"`
	OwnerID    uuid.UUID `json:"owner_id"`
	Phone      string    `json:"phone"`
	Name       string    `json:"name"`
	Email      string    `json:"email,omitempty"`
	Note       string    `json:"note,omitempty"`
	IsFavorite bool      `json:"is_favorite"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Number is the shared caller-ID profile for a phone number.
type Number struct {
	Phone        string    `json:"phone"`
	Name         string    `json:"name,omitempty"`
	Carrier      string    `json:"carrier,omitempty"`
	Country      string    `json:"country,omitempty"`
	SpamScore    int       `json:"spam_score"`
	ReportsCount int64     `json:"reports_count"`
	LookupsCount int64     `json:"lookups_count"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Tag is a community label attached to a phone number.
type Tag struct {
	ID        uuid.UUID  `json:"id"`
	Phone     string     `json:"phone"`
	Tag       string     `json:"tag"`
	Weight    int        `json:"weight"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// SpamReport is a single abuse report for a phone number.
type SpamReport struct {
	ID         uuid.UUID `json:"id"`
	Phone      string    `json:"phone"`
	ReporterID uuid.UUID `json:"reporter_id"`
	Category   string    `json:"category"`
	Comment    string    `json:"comment,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// RefreshToken is a persisted, rotated refresh-token session. The device and
// network fields are optional metadata supplied by the client at login; they
// are never required and are treated as untrusted display strings.
type RefreshToken struct {
	ID           uuid.UUID  `json:"id"`
	UserID       uuid.UUID  `json:"user_id"`
	TokenHash    string     `json:"-"`
	UserAgent    string     `json:"user_agent,omitempty"`
	IP           string     `json:"ip,omitempty"`
	DeviceID     string     `json:"device_id,omitempty"`
	DeviceName   string     `json:"device_name,omitempty"`
	DeviceModel  string     `json:"device_model,omitempty"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	OS           string     `json:"os,omitempty"`
	OSVersion    string     `json:"os_version,omitempty"`
	AppVersion   string     `json:"app_version,omitempty"`
	AppBuild     string     `json:"app_build,omitempty"`
	Client       string     `json:"client,omitempty"`
	Locale       string     `json:"locale,omitempty"`
	Timezone     string     `json:"timezone,omitempty"`
	NetworkType  string     `json:"network_type,omitempty"`
	LastIP       string     `json:"last_ip,omitempty"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Session is the safe, client-facing projection of a RefreshToken. It never
// contains the token hash or any server-side secret.
type Session struct {
	ID           uuid.UUID `json:"id"`
	Current      bool      `json:"current"`
	IP           string    `json:"ip,omitempty"`
	LastIP       string    `json:"last_ip,omitempty"`
	UserAgent    string    `json:"user_agent,omitempty"`
	DeviceID     string    `json:"device_id,omitempty"`
	DeviceName   string    `json:"device_name,omitempty"`
	DeviceModel  string    `json:"device_model,omitempty"`
	Manufacturer string    `json:"manufacturer,omitempty"`
	OS           string    `json:"os,omitempty"`
	OSVersion    string    `json:"os_version,omitempty"`
	AppVersion   string    `json:"app_version,omitempty"`
	AppBuild     string    `json:"app_build,omitempty"`
	Client       string    `json:"client,omitempty"`
	Locale       string    `json:"locale,omitempty"`
	Timezone     string    `json:"timezone,omitempty"`
	NetworkType  string    `json:"network_type,omitempty"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// TokenPair is returned after a successful authentication or rotation.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// EmailVerification is a single one-time code sent to an email address.
// The plaintext code is only kept AES-GCM encrypted for the delivery worker;
// verification compares an HMAC instead of the code itself.
type EmailVerification struct {
	ID               uuid.UUID  `json:"id"`
	Email            string     `json:"email"`
	UserID           *uuid.UUID `json:"user_id,omitempty"`
	Purpose          string     `json:"purpose"`
	CodeHash         string     `json:"-"`
	CodeCiphertext   string     `json:"-"`
	Attempts         int        `json:"attempts"`
	MaxAttempts      int        `json:"max_attempts"`
	DeliveryAttempts int        `json:"delivery_attempts"`
	RequestIP        string     `json:"request_ip,omitempty"`
	UserAgent        string     `json:"user_agent,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at"`
	ConsumedAt       *time.Time `json:"consumed_at,omitempty"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	DeliveryError    string     `json:"delivery_error,omitempty"`
	LastAttemptAt    *time.Time `json:"last_attempt_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// OutboxMessage is a Kafka event committed atomically with a business write.
type OutboxMessage struct {
	EventID      uuid.UUID  `json:"event_id"`
	EventType    string     `json:"event_type"`
	EventVersion int        `json:"event_version"`
	AggregateID  string     `json:"aggregate_id"`
	Payload      []byte     `json:"payload"`
	OccurredAt   time.Time  `json:"occurred_at"`
	CreatedAt    time.Time  `json:"created_at"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
	Attempts     int        `json:"attempts"`
	LastError    string     `json:"last_error,omitempty"`
}

// Event is an immutable fact published to Kafka.
type Event struct {
	ID          uuid.UUID `json:"id"`
	Type        string    `json:"type"`
	Version     int       `json:"version"`
	OccurredAt  time.Time `json:"occurred_at"`
	AggregateID string    `json:"aggregate_id"`
	Payload     any       `json:"payload"`
}

// Event type constants shared by API and worker.
const (
	EventUserRegistered     = "user.registered"
	EventUserBlocked        = "user.blocked"
	EventContactCreated     = "contact.created"
	EventNumberLookedUp     = "number.looked_up"
	EventTagAdded           = "number.tag_added"
	EventSpamReported       = "number.spam_reported"
	EventNumberNameAdded    = "number.name_added"
	EventNumberNameRemoved  = "number.name_removed"
	EventSpamReportDeleted  = "number.spam_deleted"
	EventAdminNumberViewed  = "admin.number_viewed"
	EventAdminNumberUpdated = "admin.number_updated"
	EventUserLoggedIn       = "user.logged_in"
	EventUserSessionRevoked = "user.session_revoked"
	EventEmailCodeRequested = "email.code_requested"
	EventEmailDelivered     = "email.delivered"
	EventEmailFailed        = "email.delivery_failed"
	EventUserEmailVerified  = "user.email_verified"
	EventUserEmailLogin     = "user.email_login"
)

// NumberName is a community-submitted name for a phone number. Each user has
// at most one vote per number; changing the vote updates this row.
type NumberName struct {
	ID          uuid.UUID  `json:"id"`
	Phone       string     `json:"phone"`
	Name        string     `json:"name"`
	SubmittedBy *uuid.UUID `json:"submitted_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// NameCount aggregates identical name votes for one number.
type NameCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// SpamCategoryCount is one bucket of the spam summary.
type SpamCategoryCount struct {
	Category string `json:"category"`
	Count    int64  `json:"count"`
}

// SpamSummary is the public, privacy-preserving spam picture for a number.
type SpamSummary struct {
	Phone          string              `json:"phone"`
	Total          int64               `json:"total"`
	ByCategory     []SpamCategoryCount `json:"by_category"`
	LastReportedAt *time.Time          `json:"last_reported_at,omitempty"`
}

// AuditEvent is a persisted Kafka event; it doubles as the admin audit trail.
type AuditEvent struct {
	ID          uuid.UUID       `json:"id"`
	Topic       string          `json:"topic"`
	Type        string          `json:"event_type"`
	AggregateID string          `json:"aggregate_id"`
	Payload     json.RawMessage `json:"payload"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ConsumedAt  time.Time       `json:"consumed_at"`
}

// AuditFilter selects a page of audit events.
type AuditFilter struct {
	EventType   string
	AggregateID string
	Page        int
	PerPage     int
}

// ContactStats is a compact per-user phone-book summary.
type ContactStats struct {
	Total     int64
	Favorites int64
}

// UserStats is the personal dashboard returned by /users/me/stats.
type UserStats struct {
	UserID         uuid.UUID `json:"user_id"`
	Contacts       int64     `json:"contacts"`
	Favorites      int64     `json:"favorites"`
	TagsAdded      int64     `json:"tags_added"`
	SpamReports    int64     `json:"spam_reports"`
	NamesSubmitted int64     `json:"names_submitted"`
	ActiveSessions int64     `json:"active_sessions"`
	MemberSince    time.Time `json:"member_since"`
}

// Pagination describes a page request.
type Pagination struct {
	Page    int
	PerPage int
}

// Offset returns a safe, non-negative row offset. It clamps the page size and
// the total offset so arithmetic cannot overflow on 32-bit builds and a hostile
// page number cannot request an unbounded deep scan.
func (p Pagination) Offset() int {
	page := p.Page
	if page < 1 {
		page = 1
	}
	perPage := p.PerPage
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	const maxOffset = 1_000_000
	if page > maxOffset/perPage {
		return maxOffset
	}
	return (page - 1) * perPage
}

// Page is a generic paginated result.
type Page[T any] struct {
	Items   []T
	Total   int64
	Page    int
	PerPage int
}

// Stats is the admin dashboard snapshot.
type Stats struct {
	Users          int64      `json:"users"`
	BlockedUsers   int64      `json:"blocked_users"`
	Contacts       int64      `json:"contacts"`
	Numbers        int64      `json:"numbers"`
	Lookups        int64      `json:"lookups"`
	SpamReports    int64      `json:"spam_reports"`
	Tags           int64      `json:"tags"`
	TopTags        []TagCount `json:"top_tags"`
	TopSpamNumbers []Number   `json:"top_spam_numbers"`
	GeneratedAt    time.Time  `json:"generated_at"`
}

// TagCount is an aggregated tag frequency.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int64  `json:"count"`
}

// Sentinel errors used by services and mapped to HTTP status codes in transport.
var (
	ErrNotFound     = errors.New("resource not found")
	ErrConflict     = errors.New("resource already exists")
	ErrUnauthorized = errors.New("authentication required")
	ErrForbidden    = errors.New("insufficient permissions")
	ErrBlocked      = errors.New("user is blocked")
	ErrInvalidToken = errors.New("invalid or expired token")
)
