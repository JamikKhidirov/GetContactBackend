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

// refreshTokenColumns is shared by every read so a newly added device field is
// automatically returned by lookup, session listing and admin summaries.
const refreshTokenColumns = `id, user_id, token_hash,
	COALESCE(user_agent, ''), COALESCE(ip, ''),
	COALESCE(device_id, ''), COALESCE(device_name, ''), COALESCE(device_model, ''),
	COALESCE(manufacturer, ''), COALESCE(os, ''), COALESCE(os_version, ''),
	COALESCE(app_version, ''), COALESCE(app_build, ''), COALESCE(client, ''),
	COALESCE(locale, ''), COALESCE(timezone, ''), COALESCE(network_type, ''),
	COALESCE(last_ip, ''), COALESCE(last_seen_at, created_at),
	expires_at, revoked_at, created_at`

func scanRefreshToken(row pgx.Row) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := row.Scan(&t.ID, &t.UserID, &t.TokenHash,
		&t.UserAgent, &t.IP,
		&t.DeviceID, &t.DeviceName, &t.DeviceModel,
		&t.Manufacturer, &t.OS, &t.OSVersion,
		&t.AppVersion, &t.AppBuild, &t.Client,
		&t.Locale, &t.Timezone, &t.NetworkType,
		&t.LastIP, &t.LastSeenAt,
		&t.ExpiresAt, &t.RevokedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Create stores a refresh token hash plus the optional client context.
func (r *TokenRepo) Create(ctx context.Context, t *domain.RefreshToken) error {
	defer observe("tokens.create", time.Now())
	lastSeen := t.LastSeenAt
	if lastSeen.IsZero() {
		lastSeen = time.Now().UTC()
	}
	lastIP := t.LastIP
	if lastIP == "" {
		lastIP = t.IP
	}
	_, err := r.db.Exec(ctx, `INSERT INTO refresh_tokens (
			id, user_id, token_hash, user_agent, ip,
			device_id, device_name, device_model, manufacturer,
			os, os_version, app_version, app_build, client,
			locale, timezone, network_type,
			last_seen_at, last_ip, last_user_agent, expires_at
		) VALUES (
			$1, $2, $3, NULLIF($4, ''), NULLIF($5, ''),
			NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''),
			NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, ''),
			NULLIF($15, ''), NULLIF($16, ''), NULLIF($17, ''),
			$18, NULLIF($19, ''), NULLIF($20, ''), $21
		)`,
		t.ID, t.UserID, t.TokenHash, t.UserAgent, t.IP,
		t.DeviceID, t.DeviceName, t.DeviceModel, t.Manufacturer,
		t.OS, t.OSVersion, t.AppVersion, t.AppBuild, t.Client,
		t.Locale, t.Timezone, t.NetworkType,
		lastSeen, lastIP, t.UserAgent, t.ExpiresAt)
	return err
}

// GetByHash returns an active (non-revoked, non-expired) token.
func (r *TokenRepo) GetByHash(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	defer observe("tokens.get_by_hash", time.Now())
	t, err := scanRefreshToken(r.db.QueryRow(ctx, `SELECT `+refreshTokenColumns+`
		FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW()`, hash))
	if errors.Is(err, domain.ErrInvalidToken) {
		return nil, err
	}
	return t, err
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

// RevokeForUser revokes one session only when it belongs to the caller.
func (r *TokenRepo) RevokeForUser(ctx context.Context, id, userID uuid.UUID) error {
	defer observe("tokens.revoke_for_user", time.Now())
	tag, err := r.db.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListActiveForUser returns active sessions newest-first, including the device
// metadata captured at login. Token hashes are loaded for rotation but never
// leave the service layer.
func (r *TokenRepo) ListActiveForUser(ctx context.Context, userID uuid.UUID) ([]domain.RefreshToken, error) {
	defer observe("tokens.list_active", time.Now())
	rows, err := r.db.Query(ctx, `SELECT `+refreshTokenColumns+`
		FROM refresh_tokens
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY COALESCE(last_seen_at, created_at) DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.RefreshToken, 0, 8)
	for rows.Next() {
		var t domain.RefreshToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.TokenHash,
			&t.UserAgent, &t.IP,
			&t.DeviceID, &t.DeviceName, &t.DeviceModel,
			&t.Manufacturer, &t.OS, &t.OSVersion,
			&t.AppVersion, &t.AppBuild, &t.Client,
			&t.Locale, &t.Timezone, &t.NetworkType,
			&t.LastIP, &t.LastSeenAt,
			&t.ExpiresAt, &t.RevokedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
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

// CountActiveForUser returns the number of live refresh sessions.
func (r *TokenRepo) CountActiveForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	defer observe("tokens.count_active", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM refresh_tokens
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`, userID).Scan(&n)
	return n, err
}

var _ domain.TokenRepository = (*TokenRepo)(nil)
