package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/platform"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

// AdminService implements back-office operations.
type AdminService struct {
	users     domain.UserRepository
	numbers   domain.NumberRepository
	contacts  domain.ContactRepository
	tokens    domain.TokenRepository
	audit     domain.AuditRepository
	cache     *rediscache.Cache
	pub       events.Publisher
	log       *slog.Logger
	accessTTL time.Duration
}

// NewAdminService wires admin use cases.
func NewAdminService(users domain.UserRepository, numbers domain.NumberRepository, contacts domain.ContactRepository, tokens domain.TokenRepository, audit domain.AuditRepository, cache *rediscache.Cache, pub events.Publisher, log *slog.Logger) *AdminService {
	return &AdminService{users: users, numbers: numbers, contacts: contacts, tokens: tokens, audit: audit, cache: cache, pub: pub, log: log, accessTTL: 15 * time.Minute}
}

// SetAccessTTL aligns the session denylist window with the access-token TTL.
func (s *AdminService) SetAccessTTL(ttl time.Duration) {
	if ttl > 0 {
		s.accessTTL = ttl
	}
}

// NumberDossier is the admin "full picture" for one number: profile, tags,
// name votes, reports and recent audit events. Every read emits an audit event.
type NumberDossier struct {
	Number     domain.Number       `json:"number"`
	Tags       []domain.Tag        `json:"tags"`
	Names      []domain.NumberName `json:"names"`
	NameCounts []domain.NameCount  `json:"name_counts"`
	Reports    []domain.SpamReport `json:"reports"`
	Spam       domain.SpamSummary  `json:"spam"`
	Audit      []domain.AuditEvent `json:"audit"`
}

// UserSummary is the admin view of one account: profile, activity counters and
// the latest audit events associated with that user.
type UserSummary struct {
	User        domain.User         `json:"user"`
	Stats       domain.UserStats    `json:"stats"`
	Sessions    []domain.Session    `json:"sessions"`
	RecentAudit []domain.AuditEvent `json:"recent_audit"`
}

// UpdateNumberInput is a partial admin edit of a number profile. Nil keeps the
// current value; an empty string clears it.
type UpdateNumberInput struct {
	Name    *string `json:"name"`
	Carrier *string `json:"carrier"`
	Country *string `json:"country"`
}

// NumberDossier returns everything the community and the platform know about a
// number. It is admin-only and every successful read is written to the audit
// trail so access to the full profile stays accountable.
func (s *AdminService) NumberDossier(ctx context.Context, phoneRaw string, actorID uuid.UUID) (*NumberDossier, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	number, err := s.numbers.Get(ctx, phone)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("number")
		}
		return nil, apperr.Internal(err)
	}

	dossier := &NumberDossier{Number: *number}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		dossier.Tags, err = s.numbers.ListTags(gctx, phone)
		return err
	})
	g.Go(func() (err error) {
		dossier.Names, err = s.numbers.ListNames(gctx, phone)
		return err
	})
	g.Go(func() (err error) {
		dossier.NameCounts, err = s.numbers.ListNameCounts(gctx, phone)
		return err
	})
	g.Go(func() (err error) {
		dossier.Reports, err = s.numbers.ListReportsForPhone(gctx, phone, 50)
		return err
	})
	g.Go(func() (err error) {
		spam, err := s.numbers.SpamSummary(gctx, phone)
		if err == nil {
			dossier.Spam = *spam
		}
		return err
	})
	g.Go(func() (err error) {
		page, err := s.audit.List(gctx, domain.AuditFilter{
			AggregateID: phone, Page: 1, PerPage: 20,
		})
		if err == nil {
			dossier.Audit = page.Items
		}
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, apperr.Internal(err)
	}

	s.publish(ctx, events.NewEvent(domain.EventAdminNumberViewed, phone, events.AdminNumberViewed{
		Phone: phone, ActorID: actorID,
	}))
	return dossier, nil
}

// Audit returns the filtered, newest-first audit feed.
func (s *AdminService) Audit(ctx context.Context, filter domain.AuditFilter) (domain.Page[domain.AuditEvent], error) {
	filter.EventType = strings.TrimSpace(filter.EventType)
	filter.AggregateID = strings.TrimSpace(filter.AggregateID)
	p := normalizePagination(filter.Page, filter.PerPage)
	filter.Page, filter.PerPage = p.Page, p.PerPage
	page, err := s.audit.List(ctx, filter)
	if err != nil {
		return page, apperr.Internal(err)
	}
	return page, nil
}

// UserSummary returns the admin view of one account together with activity
// counters and the latest audit events keyed by that user id.
func (s *AdminService) UserSummary(ctx context.Context, id uuid.UUID) (*UserSummary, error) {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("user")
		}
		return nil, apperr.Internal(err)
	}
	user.PasswordHash = ""

	summary := &UserSummary{
		User:        user.Public(),
		Stats:       domain.UserStats{UserID: user.ID, MemberSince: user.CreatedAt},
		Sessions:    make([]domain.Session, 0, 8),
		RecentAudit: make([]domain.AuditEvent, 0, 10),
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		var stats domain.ContactStats
		stats, err = s.contacts.StatsByOwner(gctx, id)
		summary.Stats.Contacts, summary.Stats.Favorites = stats.Total, stats.Favorites
		return err
	})
	g.Go(func() (err error) {
		summary.Stats.TagsAdded, err = s.numbers.CountTagsByUser(gctx, id)
		return err
	})
	g.Go(func() (err error) {
		summary.Stats.SpamReports, err = s.numbers.CountReportsByUser(gctx, id)
		return err
	})
	g.Go(func() (err error) {
		summary.Stats.NamesSubmitted, err = s.numbers.CountNamesByUser(gctx, id)
		return err
	})
	g.Go(func() (err error) {
		summary.Stats.ActiveSessions, err = s.tokens.CountActiveForUser(gctx, id)
		return err
	})
	g.Go(func() (err error) {
		active, err := s.tokens.ListActiveForUser(gctx, id)
		if err != nil {
			return err
		}
		sessions := make([]domain.Session, 0, len(active))
		for i := range active {
			sessions = append(sessions, sessionView(active[i], ""))
		}
		summary.Sessions = sessions
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, apperr.Internal(err)
	}

	// The audit feed is supplementary: a delayed worker must not make the user
	// profile unreadable, so failure is logged and the profile is still returned.
	page, err := s.audit.List(ctx, domain.AuditFilter{AggregateID: id.String(), Page: 1, PerPage: 10})
	if err != nil {
		s.log.Warn("user audit feed failed", "user_id", id, "error", err)
	} else {
		summary.RecentAudit = page.Items
	}
	return summary, nil
}

// UserSessions returns the active device sessions of any user. Admin-only and
// read-only; the admin cannot revoke a user's session from this endpoint.
func (s *AdminService) UserSessions(ctx context.Context, id uuid.UUID) ([]domain.Session, error) {
	if _, err := s.users.GetByID(ctx, id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("user")
		}
		return nil, apperr.Internal(err)
	}
	active, err := s.tokens.ListActiveForUser(ctx, id)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]domain.Session, 0, len(active))
	for i := range active {
		out = append(out, sessionView(active[i], ""))
	}
	return out, nil
}

// DeleteReport removes a spam report, recomputes the number's score and clears
// its cached caller-ID card. This is the moderation counterpart of a report.
func (s *AdminService) DeleteReport(ctx context.Context, reportID, actorID uuid.UUID) error {
	report, err := s.numbers.GetSpamReport(ctx, reportID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("report")
		}
		return apperr.Internal(err)
	}
	if err := s.numbers.DeleteSpamReport(ctx, reportID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("report")
		}
		return apperr.Internal(err)
	}
	if err := s.numbers.RecomputeSpamScore(ctx, report.Phone); err != nil {
		s.log.Warn("spam score recompute failed", "phone", platform.MaskPhone(report.Phone), "error", err)
	}
	s.invalidateNumber(ctx, report.Phone)
	s.publish(ctx, events.NewEvent(domain.EventSpamReportDeleted, report.Phone, events.SpamReportDeleted{
		ReportID: reportID, Phone: report.Phone, ActorID: actorID,
	}))
	return nil
}

// UpdateNumber applies a partial admin edit to a caller-ID profile. Nil fields
// keep their value, while an explicit empty string clears the column.
func (s *AdminService) UpdateNumber(ctx context.Context, phoneRaw string, actorID uuid.UUID, in UpdateNumberInput) (*domain.Number, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	if in.Name == nil && in.Carrier == nil && in.Country == nil {
		return nil, apperr.BadRequest("at least one of name, carrier or country is required")
	}

	fields := map[string]string{}
	name, err := normalizeAdminLabel("name", in.Name, 64)
	if err != nil {
		fields["name"] = err.Error()
	}
	carrier, err := normalizeAdminLabel("carrier", in.Carrier, 64)
	if err != nil {
		fields["carrier"] = err.Error()
	}
	country, err := normalizeAdminLabel("country", in.Country, 64)
	if err != nil {
		fields["country"] = err.Error()
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid(fields)
	}

	number, err := s.numbers.UpdateDetails(ctx, phone, name, carrier, country)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("number")
		}
		return nil, apperr.Internal(err)
	}
	s.invalidateNumber(ctx, phone)
	s.publish(ctx, events.NewEvent(domain.EventAdminNumberUpdated, phone, events.AdminNumberUpdated{
		Phone: phone, ActorID: actorID,
		Name: valueOrEmpty(name), Carrier: valueOrEmpty(carrier), Country: valueOrEmpty(country),
	}))
	return number, nil
}

// normalizeAdminLabel trims, collapses whitespace and validates an optional
// profile label. A nil pointer means "keep the current value"; a pointer to an
// empty string means "clear it".
func normalizeAdminLabel(field string, raw *string, max int) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.Join(strings.Fields(*raw), " ")
	if utf8.RuneCountInString(value) > max {
		return nil, fmt.Errorf("%s must be at most %d characters", field, max)
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return nil, fmt.Errorf("%s contains invalid characters", field)
		}
	}
	return &value, nil
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (s *AdminService) invalidateNumber(ctx context.Context, phone string) {
	if s.cache != nil {
		s.cache.Delete(ctx, numberCachePrefix+phone)
	}
}

// publish is the admin-side analogue of NumberService.publish: a failed audit
// write is logged but never fails the moderation action itself.
func (s *AdminService) publish(ctx context.Context, ev domain.Event) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.pub.Publish(pubCtx, ev); err != nil {
		s.log.Warn("admin event publish failed", "type", ev.Type, "error", err)
	}
}

// Stats aggregates dashboard counters. Queries are indexed and cheap; on a very
// large deployment this can be backed by a materialized view refreshed by the worker.
func (s *AdminService) Stats(ctx context.Context) (*domain.Stats, error) {
	users, err := s.users.Count(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	blocked, err := s.users.CountBlocked(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	contacts, err := s.contacts.Count(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	numbers, err := s.numbers.Count(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	lookups, err := s.numbers.TotalLookups(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	reports, err := s.numbers.CountReports(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	tags, err := s.numbers.CountTags(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	topTags, err := s.numbers.TopTags(ctx, 10)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	topSpam, err := s.numbers.TopSpam(ctx, 10)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &domain.Stats{
		Users: users, BlockedUsers: blocked, Contacts: contacts, Numbers: numbers,
		Lookups: lookups, SpamReports: reports, Tags: tags,
		TopTags: topTags, TopSpamNumbers: topSpam, GeneratedAt: time.Now().UTC(),
	}, nil
}

// ListUsers returns a paginated user list.
func (s *AdminService) ListUsers(ctx context.Context, page, perPage int, q string) (domain.Page[domain.User], error) {
	return s.users.List(ctx, normalizePagination(page, perPage), q)
}

// GetUser returns any user profile.
func (s *AdminService) GetUser(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("user")
		}
		return nil, apperr.Internal(err)
	}
	u.PasswordHash = ""
	return u, nil
}

// SetBlocked blocks or unblocks a user. Admins cannot block themselves.
func (s *AdminService) SetBlocked(ctx context.Context, actorID, targetID uuid.UUID, blocked bool) (*domain.User, error) {
	if actorID == targetID && blocked {
		return nil, apperr.BadRequest("you cannot block your own account")
	}
	if err := s.users.SetBlocked(ctx, targetID, blocked); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("user")
		}
		return nil, apperr.Internal(err)
	}
	u, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	u.PasswordHash = ""
	if blocked {
		if err := s.tokens.RevokeAllForUser(ctx, targetID); err != nil {
			s.log.Warn("failed to revoke sessions for blocked user", "user_id", targetID, "error", err)
		}
	}
	ev := events.NewEvent(domain.EventUserBlocked, targetID.String(), events.UserBlocked{
		UserID: targetID, Blocked: blocked, ActorID: actorID,
	})
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.pub.Publish(pubCtx, ev); err != nil {
		s.log.Warn("user.blocked publish failed", "error", err)
	}
	return u, nil
}

// DeleteUser hard-deletes an account; cascades remove its contacts and tokens.
func (s *AdminService) DeleteUser(ctx context.Context, actorID, targetID uuid.UUID) error {
	if actorID == targetID {
		return apperr.BadRequest("you cannot delete your own account via the admin API")
	}
	if err := s.users.Delete(ctx, targetID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("user")
		}
		return apperr.Internal(err)
	}
	return nil
}

// RevokeUserSessions ends every device session of a user (admin operation).
// Outstanding access tokens are denied until their TTL expires.
func (s *AdminService) RevokeUserSessions(ctx context.Context, actorID, targetID uuid.UUID) (int, error) {
	if actorID == targetID {
		return 0, apperr.BadRequest("you cannot revoke your own sessions via the admin API")
	}
	active, err := s.tokens.ListActiveForUser(ctx, targetID)
	if err != nil {
		return 0, apperr.Internal(err)
	}
	if err := s.tokens.RevokeAllForUser(ctx, targetID); err != nil {
		return 0, apperr.Internal(err)
	}
	for i := range active {
		if err := s.cache.DenySession(ctx, active[i].ID.String(), s.accessTTL); err != nil {
			s.log.Warn("session denylist failed", "session_id", active[i].ID, "error", err)
		}
	}
	for i := range active {
		ev := events.NewEvent(domain.EventUserSessionRevoked, targetID.String(), events.UserSessionRevoked{
			UserID: targetID, SessionID: active[i].ID, ActorID: actorID,
		})
		pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err := s.pub.Publish(pubCtx, ev)
		cancel()
		if err != nil {
			s.log.Warn("user.session_revoked publish failed", "error", err)
		}
	}
	return len(active), nil
}

// ListReports returns the spam-report moderation queue.
func (s *AdminService) ListReports(ctx context.Context, page, perPage int) (domain.Page[domain.SpamReport], error) {
	return s.numbers.ListReports(ctx, normalizePagination(page, perPage))
}

// ScheduleTokenCleanup periodically removes expired refresh tokens.
func (s *AdminService) ScheduleTokenCleanup(ctx context.Context, tokens domain.TokenRepository, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := tokens.DeleteExpired(ctx, time.Now().UTC().Add(-24*time.Hour))
			if err != nil {
				s.log.Warn("refresh token cleanup failed", "error", err)
				continue
			}
			if n > 0 {
				s.log.Info("expired refresh tokens removed", "count", n)
			}
		}
	}
}
