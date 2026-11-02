package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/platform"
	"github.com/google/uuid"
)

// ContactService implements the private phone book.
type ContactService struct {
	contacts domain.ContactRepository
	pub      events.Publisher
	log      *slog.Logger
}

// NewContactService wires contact use cases.
func NewContactService(contacts domain.ContactRepository, pub events.Publisher, log *slog.Logger) *ContactService {
	return &ContactService{contacts: contacts, pub: pub, log: log}
}

// ContactInput is the create/update payload.
type ContactInput struct {
	Phone      string `json:"phone"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Note       string `json:"note"`
	IsFavorite bool   `json:"is_favorite"`
}

// UpdateContactInput is a partial update.
type UpdateContactInput struct {
	Phone      *string `json:"phone"`
	Name       *string `json:"name"`
	Email      *string `json:"email"`
	Note       *string `json:"note"`
	IsFavorite *bool   `json:"is_favorite"`
}

// ContactListQuery captures list filters.
type ContactListQuery struct {
	Q        string
	Favorite *bool
	Page     int
	PerPage  int
}

// List returns the caller's paginated phone book.
func (s *ContactService) List(ctx context.Context, ownerID uuid.UUID, q ContactListQuery) (domain.Page[domain.Contact], error) {
	p := normalizePagination(q.Page, q.PerPage)
	return s.contacts.List(ctx, ownerID, strings.TrimSpace(q.Q), q.Favorite, p)
}

// Get returns one contact owned by the caller.
func (s *ContactService) Get(ctx context.Context, ownerID, contactID uuid.UUID) (*domain.Contact, error) {
	c, err := s.contacts.GetByID(ctx, contactID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("contact")
		}
		return nil, apperr.Internal(err)
	}
	if c.OwnerID != ownerID {
		return nil, apperr.NotFound("contact") // do not leak foreign resources
	}
	return c, nil
}

// Create adds a contact to the caller's phone book.
func (s *ContactService) Create(ctx context.Context, ownerID uuid.UUID, in ContactInput) (*domain.Contact, error) {
	phone, fields := validateContact(in)
	if len(fields) > 0 {
		return nil, apperr.Invalid(fields)
	}
	c := &domain.Contact{
		ID: uuid.New(), OwnerID: ownerID, Phone: phone, Name: strings.TrimSpace(in.Name),
		Email: strings.TrimSpace(in.Email), Note: strings.TrimSpace(in.Note), IsFavorite: in.IsFavorite,
	}
	if err := s.contacts.Create(ctx, c); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, apperr.Conflict("contact with this phone already exists")
		}
		return nil, apperr.Internal(err)
	}
	ev := events.NewEvent(domain.EventContactCreated, c.ID.String(), events.ContactCreated{
		ContactID: c.ID, OwnerID: ownerID, Phone: c.Phone, Name: c.Name,
	})
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.pub.Publish(pubCtx, ev); err != nil {
		s.log.Warn("contact.created publish failed", "error", err)
	}
	return c, nil
}

// Update applies a partial update to an owned contact.
func (s *ContactService) Update(ctx context.Context, ownerID, contactID uuid.UUID, in UpdateContactInput) (*domain.Contact, error) {
	c, err := s.Get(ctx, ownerID, contactID)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{}
	if in.Phone != nil {
		phone, err := platform.NormalizePhone(*in.Phone)
		if err != nil {
			fields["phone"] = "must be a valid phone number"
		} else {
			c.Phone = phone
		}
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if n := utf8.RuneCountInString(name); n < 1 || n > 128 {
			fields["name"] = "must be between 1 and 128 characters"
		} else {
			c.Name = name
		}
	}
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if err := platform.ValidateEmail(email); err != nil {
			fields["email"] = err.Error()
		} else {
			c.Email = email
		}
	}
	if in.Note != nil {
		note := strings.TrimSpace(*in.Note)
		if utf8.RuneCountInString(note) > 1000 {
			fields["note"] = "must be at most 1000 characters"
		} else {
			c.Note = note
		}
	}
	if in.IsFavorite != nil {
		c.IsFavorite = *in.IsFavorite
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid(fields)
	}
	if err := s.contacts.Update(ctx, c); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, apperr.Conflict("contact with this phone already exists")
		}
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("contact")
		}
		return nil, apperr.Internal(err)
	}
	return c, nil
}

// Delete removes an owned contact.
func (s *ContactService) Delete(ctx context.Context, ownerID, contactID uuid.UUID) error {
	if err := s.contacts.Delete(ctx, contactID, ownerID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("contact")
		}
		return apperr.Internal(err)
	}
	return nil
}

// ImportResult reports a bulk import.
type ImportResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// Import bulk-upserts up to 500 contacts in a single database batch.
func (s *ContactService) Import(ctx context.Context, ownerID uuid.UUID, items []ContactInput) (*ImportResult, error) {
	if len(items) == 0 {
		return nil, apperr.Invalid(map[string]string{"contacts": "at least one contact is required"})
	}
	if len(items) > 500 {
		return nil, apperr.Invalid(map[string]string{"contacts": "at most 500 contacts per request"})
	}
	rows := make([]domain.Contact, 0, len(items))
	details := map[string]string{}
	for i, in := range items {
		phone, fields := validateContact(in)
		if len(fields) > 0 {
			for f, msg := range fields {
				details["contacts["+strconv.Itoa(i)+"]."+f] = msg
			}
			continue
		}
		rows = append(rows, domain.Contact{
			ID: uuid.New(), OwnerID: ownerID, Phone: phone,
			Name: strings.TrimSpace(in.Name), Email: strings.TrimSpace(in.Email),
			Note: strings.TrimSpace(in.Note), IsFavorite: in.IsFavorite,
		})
	}
	if len(details) > 0 {
		return nil, apperr.Invalid(details)
	}
	ids, err := s.contacts.BulkUpsert(ctx, ownerID, rows)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	// One event per stored contact keeps the audit trail as detailed as the
	// single-contact create path without adding a round-trip per row.
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for i, id := range ids {
		ev := events.NewEvent(domain.EventContactCreated, id.String(), events.ContactCreated{
			ContactID: id, OwnerID: ownerID, Phone: rows[i].Phone, Name: rows[i].Name,
		})
		if err := s.pub.Publish(pubCtx, ev); err != nil {
			s.log.Warn("contact.created publish failed (import)", "error", err)
		}
	}
	return &ImportResult{Imported: len(ids), Skipped: len(items) - len(ids)}, nil
}

func validateContact(in ContactInput) (string, map[string]string) {
	fields := map[string]string{}
	phone, err := platform.NormalizePhone(in.Phone)
	if err != nil {
		fields["phone"] = "must be a valid phone number (8..15 digits)"
	}
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 128 {
		fields["name"] = "must be between 1 and 128 characters"
	}
	if err := platform.ValidateEmail(strings.TrimSpace(in.Email)); err != nil {
		fields["email"] = err.Error()
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Note)) > 1000 {
		fields["note"] = "must be at most 1000 characters"
	}
	return phone, fields
}

func normalizePagination(page, perPage int) domain.Pagination {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	return domain.Pagination{Page: page, PerPage: perPage}
}
