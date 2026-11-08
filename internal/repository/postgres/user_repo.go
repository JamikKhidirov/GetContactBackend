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

// UserRepo is the pgx implementation of domain.UserRepository.
type UserRepo struct{ db *pgxpool.Pool }

// NewUserRepo builds a user repository.
func NewUserRepo(db *pgxpool.Pool) *UserRepo { return &UserRepo{db: db} }

const userColumns = `id, phone, COALESCE(email, ''), email_verified, email_verified_at, password_hash, display_name, role, is_blocked, created_at, updated_at`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	var role string
	if err := row.Scan(&u.ID, &u.Phone, &u.Email, &u.EmailVerified, &u.EmailVerifiedAt,
		&u.PasswordHash, &u.DisplayName, &role, &u.IsBlocked, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	u.Role = domain.Role(role)
	return &u, nil
}

// Create inserts a user, mapping unique violations to domain.ErrConflict.
func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	defer observe("users.create", time.Now())
	err := r.db.QueryRow(ctx, `INSERT INTO users
			(id, phone, email, email_verified, email_verified_at, password_hash, display_name, role, is_blocked)
		VALUES ($1, $2, NULLIF($3, ''), $4, CASE WHEN $4 THEN NOW() ELSE NULL END, $5, $6, $7, $8)
		RETURNING created_at, updated_at`,
		u.ID, u.Phone, u.Email, u.EmailVerified, u.PasswordHash, u.DisplayName, string(u.Role), u.IsBlocked,
	).Scan(&u.CreatedAt, &u.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

// GetByID fetches a user by primary key.
func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	defer observe("users.get_by_id", time.Now())
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// GetByPhone fetches a user by normalised phone number.
func (r *UserRepo) GetByPhone(ctx context.Context, phone string) (*domain.User, error) {
	defer observe("users.get_by_phone", time.Now())
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE phone = $1`, phone))
}

// GetByEmail fetches a user by case-insensitive email.
func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	defer observe("users.get_by_email", time.Now())
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userColumns+`
		FROM users WHERE LOWER(email) = LOWER($1) AND email IS NOT NULL AND email <> ''`, email))
}

// Update persists mutable profile fields. Changing the email address resets its
// verified flag: ownership must be proven again at the new address.
func (r *UserRepo) Update(ctx context.Context, u *domain.User) error {
	defer observe("users.update", time.Now())
	err := r.db.QueryRow(ctx, `UPDATE users
		SET display_name = $2,
		    email_verified = CASE WHEN email IS DISTINCT FROM NULLIF($3, '') THEN FALSE ELSE email_verified END,
		    email_verified_at = CASE WHEN email IS DISTINCT FROM NULLIF($3, '') THEN NULL ELSE email_verified_at END,
		    email = NULLIF($3, '')
		WHERE id = $1
		RETURNING updated_at`, u.ID, u.DisplayName, u.Email).Scan(&u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

// SetEmailVerified binds an address to a user after the code was confirmed.
func (r *UserRepo) SetEmailVerified(ctx context.Context, id uuid.UUID, email string) error {
	defer observe("users.set_email_verified", time.Now())
	tag, err := r.db.Exec(ctx, `UPDATE users
		SET email = $2, email_verified = TRUE, email_verified_at = NOW()
		WHERE id = $1`, id, email)
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

// Delete removes a user; related rows cascade.
func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	defer observe("users.delete", time.Now())
	tag, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// List returns a filtered, paginated user list for the admin area.
func (r *UserRepo) List(ctx context.Context, p domain.Pagination, query string) (domain.Page[domain.User], error) {
	defer observe("users.list", time.Now())
	page := domain.Page[domain.User]{Page: p.Page, PerPage: p.PerPage}
	where := `WHERE ($1 = '' OR phone ILIKE '%' || $1 || '%' OR display_name ILIKE '%' || $1 || '%')`
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users `+where, query).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := r.db.Query(ctx, `SELECT `+userColumns+` FROM users `+where+
		` ORDER BY created_at DESC LIMIT $2 OFFSET $3`, query, p.PerPage, p.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	page.Items = make([]domain.User, 0, p.PerPage)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, *u)
	}
	return page, rows.Err()
}

// Count returns the total number of users.
func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	defer observe("users.count", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountBlocked returns the number of blocked users.
func (r *UserRepo) CountBlocked(ctx context.Context) (int64, error) {
	defer observe("users.count_blocked", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE is_blocked`).Scan(&n)
	return n, err
}

// SetBlocked toggles the blocked flag (admin operation).
func (r *UserRepo) SetBlocked(ctx context.Context, id uuid.UUID, blocked bool) error {
	defer observe("users.set_blocked", time.Now())
	tag, err := r.db.Exec(ctx, `UPDATE users SET is_blocked = $2 WHERE id = $1`, id, blocked)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdatePassword replaces the password hash and revokes nothing by itself.
func (r *UserRepo) UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error {
	defer observe("users.update_password", time.Now())
	tag, err := r.db.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetRole changes a user's authorization role.
func (r *UserRepo) SetRole(ctx context.Context, id uuid.UUID, role domain.Role) error {
	defer observe("users.set_role", time.Now())
	tag, err := r.db.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, string(role))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Ensure interface conformance at compile time.
var _ domain.UserRepository = (*UserRepo)(nil)
