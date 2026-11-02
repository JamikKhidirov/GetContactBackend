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

// NumberRepo is the pgx implementation of domain.NumberRepository.
type NumberRepo struct{ db *pgxpool.Pool }

// NewNumberRepo builds a caller-ID repository.
func NewNumberRepo(db *pgxpool.Pool) *NumberRepo { return &NumberRepo{db: db} }

const numberColumns = `phone, COALESCE(name, ''), COALESCE(carrier, ''), COALESCE(country, ''),
	spam_score, reports_count, lookups_count, updated_at`

func scanNumber(row pgx.Row) (*domain.Number, error) {
	var n domain.Number
	if err := row.Scan(&n.Phone, &n.Name, &n.Carrier, &n.Country,
		&n.SpamScore, &n.ReportsCount, &n.LookupsCount, &n.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &n, nil
}

// Get fetches a number profile.
func (r *NumberRepo) Get(ctx context.Context, phone string) (*domain.Number, error) {
	defer observe("numbers.get", time.Now())
	return scanNumber(r.db.QueryRow(ctx, `SELECT `+numberColumns+` FROM numbers WHERE phone = $1`, phone))
}

// Upsert creates or enriches a number profile without erasing existing data.
func (r *NumberRepo) Upsert(ctx context.Context, n *domain.Number) error {
	defer observe("numbers.upsert", time.Now())
	return r.db.QueryRow(ctx, `INSERT INTO numbers (phone, name, carrier, country)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''))
		ON CONFLICT (phone) DO UPDATE SET
			name    = COALESCE(NULLIF(EXCLUDED.name, ''), numbers.name),
			carrier = COALESCE(NULLIF(EXCLUDED.carrier, ''), numbers.carrier),
			country = COALESCE(NULLIF(EXCLUDED.country, ''), numbers.country)
		RETURNING phone, COALESCE(name, ''), COALESCE(carrier, ''), COALESCE(country, ''),
			spam_score, reports_count, lookups_count, updated_at`,
		n.Phone, n.Name, n.Carrier, n.Country,
	).Scan(&n.Phone, &n.Name, &n.Carrier, &n.Country, &n.SpamScore, &n.ReportsCount, &n.LookupsCount, &n.UpdatedAt)
}

// IncrementLookups atomically bumps the lookup counter without blocking reads.
func (r *NumberRepo) IncrementLookups(ctx context.Context, phone string) error {
	defer observe("numbers.increment_lookups", time.Now())
	_, err := r.db.Exec(ctx, `UPDATE numbers SET lookups_count = lookups_count + 1 WHERE phone = $1`, phone)
	return err
}

const tagColumns = `id, phone, tag, weight, created_by, created_at`

func scanTag(row pgx.Row) (*domain.Tag, error) {
	var t domain.Tag
	if err := row.Scan(&t.ID, &t.Phone, &t.Tag, &t.Weight, &t.CreatedBy, &t.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// ListTags returns tags ordered by community weight.
func (r *NumberRepo) ListTags(ctx context.Context, phone string) ([]domain.Tag, error) {
	defer observe("tags.list", time.Now())
	rows, err := r.db.Query(ctx, `SELECT `+tagColumns+` FROM tags
		WHERE phone = $1 ORDER BY weight DESC, created_at ASC`, phone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := make([]domain.Tag, 0, 8)
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		tags = append(tags, *t)
	}
	return tags, rows.Err()
}

// AddTag inserts a tag or increments its weight atomically.
func (r *NumberRepo) AddTag(ctx context.Context, t *domain.Tag) error {
	defer observe("tags.add", time.Now())
	err := r.db.QueryRow(ctx, `INSERT INTO tags (id, phone, tag, weight, created_by)
		VALUES ($1, $2, $3, 1, $4)
		ON CONFLICT (phone, tag) DO UPDATE SET weight = tags.weight + 1
		RETURNING id, weight, created_at, created_by`,
		t.ID, t.Phone, t.Tag, t.CreatedBy,
	).Scan(&t.ID, &t.Weight, &t.CreatedAt, &t.CreatedBy)
	if isForeignKeyViolation(err) {
		return domain.ErrNotFound
	}
	return err
}

// GetTag fetches one tag by id.
func (r *NumberRepo) GetTag(ctx context.Context, id uuid.UUID) (*domain.Tag, error) {
	defer observe("tags.get", time.Now())
	return scanTag(r.db.QueryRow(ctx, `SELECT `+tagColumns+` FROM tags WHERE id = $1`, id))
}

// DeleteTag removes a tag by id.
func (r *NumberRepo) DeleteTag(ctx context.Context, id uuid.UUID) error {
	defer observe("tags.delete", time.Now())
	tag, err := r.db.Exec(ctx, `DELETE FROM tags WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AddSpamReport inserts a report and refreshes the denormalised report counter.
func (r *NumberRepo) AddSpamReport(ctx context.Context, rep *domain.SpamReport) error {
	defer observe("spam_reports.add", time.Now())
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `INSERT INTO spam_reports (id, phone, reporter_id, category, comment)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))`,
		rep.ID, rep.Phone, rep.ReporterID, rep.Category, rep.Comment); err != nil {
		if isUniqueViolation(err) {
			return domain.ErrConflict
		}
		if isForeignKeyViolation(err) {
			return domain.ErrNotFound
		}
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE numbers
		SET reports_count = (SELECT COUNT(*) FROM spam_reports WHERE phone = $1)
		WHERE phone = $1`, rep.Phone); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecomputeSpamScore derives the 0..100 score from reports and risky tags.
func (r *NumberRepo) RecomputeSpamScore(ctx context.Context, phone string) error {
	defer observe("numbers.recompute_spam", time.Now())
	_, err := r.db.Exec(ctx, `
		UPDATE numbers n SET
			reports_count = (SELECT COUNT(*) FROM spam_reports WHERE phone = n.phone),
			spam_score = LEAST(100,
				(SELECT COUNT(*) * 12 FROM spam_reports WHERE phone = n.phone)
				+ LEAST(40, (
					SELECT COALESCE(SUM(weight), 0) * 3 FROM tags
					WHERE phone = n.phone AND tag IN (
						'spam', 'scam', 'fraud', 'telemarketing', 'robocall',
						'спам', 'мошенник', 'мошенники', 'реклама', 'коллектор'
					)
				))
			)
		WHERE n.phone = $1`, phone)
	return err
}

// TopSpam returns the most reported numbers.
func (r *NumberRepo) TopSpam(ctx context.Context, limit int) ([]domain.Number, error) {
	defer observe("numbers.top_spam", time.Now())
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := r.db.Query(ctx, `SELECT `+numberColumns+` FROM numbers
		WHERE spam_score > 0 OR reports_count > 0
		ORDER BY spam_score DESC, reports_count DESC, lookups_count DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Number, 0, limit)
	for rows.Next() {
		n, err := scanNumber(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// Search performs a prefix/contains search over phone and name.
func (r *NumberRepo) Search(ctx context.Context, q string, limit int) ([]domain.Number, error) {
	defer observe("numbers.search", time.Now())
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	pattern := "%" + q + "%"
	rows, err := r.db.Query(ctx, `SELECT `+numberColumns+` FROM numbers
		WHERE phone ILIKE $1 OR name ILIKE $1
		ORDER BY spam_score DESC, lookups_count DESC LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Number, 0, limit)
	for rows.Next() {
		n, err := scanNumber(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// Count returns the number of known number profiles.
func (r *NumberRepo) Count(ctx context.Context) (int64, error) {
	defer observe("numbers.count", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM numbers`).Scan(&n)
	return n, err
}

// TotalLookups returns the sum of all lookup counters.
func (r *NumberRepo) TotalLookups(ctx context.Context) (int64, error) {
	defer observe("numbers.total_lookups", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COALESCE(SUM(lookups_count), 0) FROM numbers`).Scan(&n)
	return n, err
}

// CountReports returns the total number of spam reports.
func (r *NumberRepo) CountReports(ctx context.Context) (int64, error) {
	defer observe("spam_reports.count", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM spam_reports`).Scan(&n)
	return n, err
}

// CountTags returns the total number of distinct number/tag pairs.
func (r *NumberRepo) CountTags(ctx context.Context) (int64, error) {
	defer observe("tags.count", time.Now())
	var n int64
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM tags`).Scan(&n)
	return n, err
}

// TopTags returns the most frequently applied community tags.
func (r *NumberRepo) TopTags(ctx context.Context, limit int) ([]domain.TagCount, error) {
	defer observe("tags.top", time.Now())
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := r.db.Query(ctx, `SELECT tag, COUNT(*) AS c FROM tags
		GROUP BY tag ORDER BY c DESC, tag ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TagCount, 0, limit)
	for rows.Next() {
		var tc domain.TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

var _ domain.NumberRepository = (*NumberRepo)(nil)

// ListReports returns all spam reports, newest first (admin endpoint).
func (r *NumberRepo) ListReports(ctx context.Context, p domain.Pagination) (domain.Page[domain.SpamReport], error) {
	defer observe("spam_reports.list", time.Now())
	page := domain.Page[domain.SpamReport]{Page: p.Page, PerPage: p.PerPage}
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM spam_reports`).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := r.db.Query(ctx, `SELECT id, phone, reporter_id, category, COALESCE(comment, ''), created_at
		FROM spam_reports ORDER BY created_at DESC LIMIT $1 OFFSET $2`, p.PerPage, p.Offset())
	if err != nil {
		return page, err
	}
	defer rows.Close()
	page.Items = make([]domain.SpamReport, 0, p.PerPage)
	for rows.Next() {
		var rep domain.SpamReport
		if err := rows.Scan(&rep.ID, &rep.Phone, &rep.ReporterID, &rep.Category, &rep.Comment, &rep.CreatedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, rep)
	}
	return page, rows.Err()
}
