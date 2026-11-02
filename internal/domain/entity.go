// Package domain contains the business entities, invariants and repository ports.
// It has no dependency on transport, database or framework packages.
package domain

import (
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
	ID           uuid.UUID `json:"id"`
	Phone        string    `json:"phone"`
	Email        string    `json:"email,omitempty"`
	PasswordHash string    `json:"-"`
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

// RefreshToken is a persisted, rotated refresh-token session.
type RefreshToken struct {
	ID        uuid.UUID  `json:"id"`
	UserID    uuid.UUID  `json:"user_id"`
	TokenHash string     `json:"-"`
	UserAgent string     `json:"user_agent,omitempty"`
	IP        string     `json:"ip,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// TokenPair is returned after a successful authentication or rotation.
type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
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
	EventUserRegistered = "user.registered"
	EventUserBlocked    = "user.blocked"
	EventContactCreated = "contact.created"
	EventNumberLookedUp = "number.looked_up"
	EventTagAdded       = "number.tag_added"
	EventSpamReported   = "number.spam_reported"
)

// Pagination describes a page request.
type Pagination struct {
	Page    int
	PerPage int
}

// Offset returns the SQL offset for the requested page.
func (p Pagination) Offset() int { return (p.Page - 1) * p.PerPage }

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
