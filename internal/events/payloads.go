package events

import (
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/google/uuid"
)

// NewEvent wraps a payload into a versioned event envelope.
func NewEvent(eventType, aggregateID string, payload any) domain.Event {
	return domain.Event{
		ID:          uuid.New(),
		Type:        eventType,
		Version:     1,
		OccurredAt:  time.Now().UTC(),
		AggregateID: aggregateID,
		Payload:     payload,
	}
}

// UserRegistered is emitted after a successful registration.
type UserRegistered struct {
	UserID      uuid.UUID `json:"user_id"`
	Phone       string    `json:"phone"`
	DisplayName string    `json:"display_name"`
}

// UserBlocked is emitted when an admin changes a user's block state.
type UserBlocked struct {
	UserID  uuid.UUID `json:"user_id"`
	Blocked bool      `json:"blocked"`
	ActorID uuid.UUID `json:"actor_id"`
}

// ContactCreated is emitted after a contact is created or imported.
type ContactCreated struct {
	ContactID uuid.UUID `json:"contact_id"`
	OwnerID   uuid.UUID `json:"owner_id"`
	Phone     string    `json:"phone"`
	Name      string    `json:"name"`
}

// NumberLookedUp is emitted for caller-ID lookups (sampled by the API).
type NumberLookedUp struct {
	Phone  string     `json:"phone"`
	UserID *uuid.UUID `json:"user_id,omitempty"`
	Result string     `json:"result"` // cache_hit | cache_miss
}

// TagAdded is emitted when a community tag is applied.
type TagAdded struct {
	Phone  string    `json:"phone"`
	Tag    string    `json:"tag"`
	Weight int       `json:"weight"`
	UserID uuid.UUID `json:"user_id"`
}

// SpamReported is emitted when a spam report is accepted.
type SpamReported struct {
	ReportID   uuid.UUID `json:"report_id"`
	Phone      string    `json:"phone"`
	Category   string    `json:"category"`
	ReporterID uuid.UUID `json:"reporter_id"`
}
