package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ContactRepo is the pgx implementation of domain.ContactRepository.
type ContactRepo struct{ db *pgxpool.Pool }

// NewContactRepo builds a contact repository.
func NewContactRepo(db *pgxpool.Pool) *ContactRepo { return &ContactRepo{db: db} }

const contactColumns = `id, owner_id, phone, name, COALESCE(email, ''), COALESCE(note, ''), is_favorite, created_at, updated_at`

func scanContact(row pgx.Row) (*domain.Contact, error) {
	var c domain.Contact
	if err := row.Scan(&c.ID, &c.OwnerID, &c.Phone, &c.Name, &c.Email, &c.Note, &c.IsFavorite, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

// Create inserts a contact and maps duplicates to domain.ErrConflict.
func (r *ContactRepo) Create(ctx context.Context, c *domain.Contact) error {
	defer observe("contacts.create", time.Now())
	err := r.db.QueryRow(ctx, `INSERT INTO contacts (id, owner_id, phone, name, email, note, is_favorite)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7)
		RETURNING created_at, updated_at`,
		c.ID, c.OwnerID, c.Phone, c.Name, c.Email, c.Note, c.IsFavorite,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

// GetByID fetches a contact without ownership filtering (service enforces it).
func (r *ContactRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Contact, error) {
	defer observe("contacts.get_by_id", time.Now())
	return scanContact(r.db.QueryRow(ctx, `SELECT `+contactColumns+` FROM contacts WHERE id = $1`, id))
}

// List returns a filtered, paginated phone book.
func (r *ContactRepo) List(ctx context.Context, ownerID uuid.UUID, q string, favorite *bool, p domain.Pagination) (domain.Page[domain.Contact], error) {
	defer observe("contacts.list", time.Now())
	page := domain.Page[domain.Contact]{Page: p.Page, PerPage: p.PerPage}
	const where = `WHERE owner_id = $1
		AND ($2 = '' OR phone ILIKE '%' || $2 || '%' OR name ILIKE '%' || $2 || '%')
		AND ($3::boolean IS NULL OR is_favorite = $3::boolean)`

	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM contacts `+where, ownerID, q, favorite).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := r.db.Query(ctx, `SELECT `+contactColumns+` FROM contacts `+where+
		` ORDER BY is_favorite DESC, LOWER(name) ASC LIMIT $4 OFFSET $5`,
		ownerID, q, favorite, p.PerPage, p.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	page.Items = make([]domain.Contact, 0, p.PerPage)
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, *c)
	}
	return page, rows.Err()
}

// Update modifies mutable contact fields after the service has checked ownership.
func (r *ContactRepo) Update(ctx context.Context, c *domain.Contact) error {
	defer observe("contacts.update", time.Now())
	err := r.db.QueryRow(ctx, `UPDATE contacts
		SET phone = $2, name = $3, email = NULLIF($4, ''), note = NULLIF($5, ''), is_favorite = $6
		WHERE id = $1 AND owner_id = $7
		RETURNING updated_at`,
		c.ID, c.Phone, c.Name, c.Email, c.Note, c.IsFavorite, c.OwnerID).Scan(&c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

// Delete removes a contact owned by ownerID.
func (r *ContactRepo) Delete(ctx context.Context, id, ownerID uuid.UUID) error {
	defer observe("contacts.delete", time.Now())
	tag, err := r.db.Exec(ctx, `DELETE FROM contacts WHERE id = $1 AND owner_id = $2`, id, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// BulkUpsert imports up to N contacts in one round-trip using a batch and
// returns the stored row IDs in input order (upsert keeps the existing ID).
func (r *ContactRepo) BulkUpsert(ctx context.Context, ownerID uuid.UUID, items []domain.Contact) ([]uuid.UUID, error) {
	defer observe("contacts.bulk_upsert", time.Now())
	if len(items) == 0 {
		return nil, nil
	}
	batch := &pgx.Batch{}
	const q = `INSERT INTO contacts (id, owner_id, phone, name, email, note, is_favorite)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7)
		ON CONFLICT (owner_id, phone) DO UPDATE
		SET name = EXCLUDED.name, email = EXCLUDED.email, note = EXCLUDED.note,
		    is_favorite = EXCLUDED.is_favorite, updated_at = NOW()
		RETURNING id`
	for i := range items {
		batch.Queue(q, items[i].ID, ownerID, items[i].Phone, items[i].Name,
			items[i].Email, items[i].Note, items[i].IsFavorite)
	}
	br := r.db.SendBatch(ctx, batch)
	defer br.Close()

	ids := make([]uuid.UUID, 0, len(items))
	for range items {
		var id uuid.UUID
		if err := br.QueryRow().Scan(&id); err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Count returns the total number of contacts.
func (r *ContactRepo) Count(ctx context.Context) (int64, error) {
	defer observe("contacts.count", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM contacts`).Scan(&n)
	return n, err
}

// StatsByOwner returns the size of a user's phone book and favourite count.
func (r *ContactRepo) StatsByOwner(ctx context.Context, ownerID uuid.UUID) (domain.ContactStats, error) {
	defer observe("contacts.stats_by_owner", time.Now())
	var s domain.ContactStats
	err := r.db.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE is_favorite)
		FROM contacts WHERE owner_id = $1`, ownerID).Scan(&s.Total, &s.Favorites)
	return s, err
}

var _ domain.ContactRepository = (*ContactRepo)(nil)
