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

// TokenRepo persists hashed refresh tokens.
type TokenRepo struct{ db *pgxpool.Pool }

// NewTokenRepo builds a refresh-token repository.
func NewTokenRepo(db *pgxpool.Pool) *TokenRepo { return &TokenRepo{db: db} }

// Create stores a refresh token hash.
func (r *TokenRepo) Create(ctx context.Context, t *domain.RefreshToken) error {
	defer observe("tokens.create", time.Now())
	_, err := r.db.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, token_hash, user_agent, ip, expires_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6)`,
		t.ID, t.UserID, t.TokenHash, t.UserAgent, t.IP, t.ExpiresAt)
	return err
}

// GetByHash returns an active (non-revoked, non-expired) token.
func (r *TokenRepo) GetByHash(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	defer observe("tokens.get_by_hash", time.Now())
	var t domain.RefreshToken
	err := r.db.QueryRow(ctx, `SELECT id, user_id, token_hash, COALESCE(user_agent, ''), COALESCE(ip, ''),
		expires_at, revoked_at, created_at
		FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW()`, hash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.UserAgent, &t.IP, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Revoke marks a single token as used.
func (r *TokenRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	defer observe("tokens.revoke", time.Now())
	_, err := r.db.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// RevokeAllForUser revokes every active session (logout-all / password change).
func (r *TokenRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	defer observe("tokens.revoke_all", time.Now())
	_, err := r.db.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return err
}

// DeleteExpired garbage-collects old rows.
func (r *TokenRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	defer observe("tokens.delete_expired", time.Now())
	tag, err := r.db.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

var _ domain.TokenRepository = (*TokenRepo)(nil)
