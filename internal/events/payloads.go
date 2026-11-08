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

// NumberNameAdded is emitted when a user adds or changes a name vote.
type NumberNameAdded struct {
	NameID uuid.UUID `json:"name_id"`
	Phone  string    `json:"phone"`
	Name   string    `json:"name"`
	UserID uuid.UUID `json:"user_id"`
}

// NumberNameRemoved is emitted when a name vote is deleted.
type NumberNameRemoved struct {
	NameID  uuid.UUID `json:"name_id"`
	Phone   string    `json:"phone"`
	ActorID uuid.UUID `json:"actor_id"`
}

// SpamReportDeleted is emitted when an admin removes a spam report.
type SpamReportDeleted struct {
	ReportID uuid.UUID `json:"report_id"`
	Phone    string    `json:"phone"`
	ActorID  uuid.UUID `json:"actor_id"`
}

// AdminNumberViewed records every admin dossier read for accountability.
type AdminNumberViewed struct {
	Phone   string    `json:"phone"`
	ActorID uuid.UUID `json:"actor_id"`
}

// AdminNumberUpdated is emitted after an admin edits a number profile.
type AdminNumberUpdated struct {
	Phone   string    `json:"phone"`
	ActorID uuid.UUID `json:"actor_id"`
	Name    string    `json:"name,omitempty"`
	Carrier string    `json:"carrier,omitempty"`
	Country string    `json:"country,omitempty"`
}

// UserLoggedIn captures consent-based client context (IP, device and app
// version) for the security audit trail. It never contains contacts, media or
// any other data read from the device.
type UserLoggedIn struct {
	UserID       uuid.UUID `json:"user_id"`
	SessionID    uuid.UUID `json:"session_id"`
	IP           string    `json:"ip,omitempty"`
	UserAgent    string    `json:"user_agent,omitempty"`
	DeviceID     string    `json:"device_id,omitempty"`
	DeviceName   string    `json:"device_name,omitempty"`
	DeviceModel  string    `json:"device_model,omitempty"`
	Manufacturer string    `json:"manufacturer,omitempty"`
	OS           string    `json:"os,omitempty"`
	OSVersion    string    `json:"os_version,omitempty"`
	AppVersion   string    `json:"app_version,omitempty"`
	Client       string    `json:"client,omitempty"`
}

// UserSessionRevoked is emitted when a user or admin ends a device session.
type UserSessionRevoked struct {
	UserID    uuid.UUID `json:"user_id"`
	SessionID uuid.UUID `json:"session_id"`
	ActorID   uuid.UUID `json:"actor_id"`
}

// EmailCodeRequested asks the worker to deliver a one-time code. The code is
// deliberately absent: the worker loads and decrypts it from the database, so
// the Kafka topic and audit log never contain a usable credential.
type EmailCodeRequested struct {
	VerificationID uuid.UUID `json:"verification_id"`
	Email          string    `json:"email"`
	Purpose        string    `json:"purpose"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// EmailDelivered records a successful hand-off to the mail provider.
type EmailDelivered struct {
	VerificationID uuid.UUID `json:"verification_id"`
	Email          string    `json:"email"`
	Purpose        string    `json:"purpose"`
	Provider       string    `json:"provider"`
}

// EmailDeliveryFailed records an unrecoverable delivery failure.
type EmailDeliveryFailed struct {
	VerificationID uuid.UUID `json:"verification_id"`
	Email          string    `json:"email"`
	Purpose        string    `json:"purpose"`
	Provider       string    `json:"provider"`
	Error          string    `json:"error"`
}

// UserEmailVerified is emitted after a user proves ownership of an address.
type UserEmailVerified struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
}

// UserEmailLogin is emitted after a successful passwordless email login.
type UserEmailLogin struct {
	UserID    uuid.UUID `json:"user_id"`
	SessionID uuid.UUID `json:"session_id"`
	Email     string    `json:"email"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	Client    string    `json:"client,omitempty"`
}
