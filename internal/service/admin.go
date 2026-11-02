package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/google/uuid"
)

// AdminService implements back-office operations.
type AdminService struct {
	users    domain.UserRepository
	numbers  domain.NumberRepository
	contacts domain.ContactRepository
	tokens   domain.TokenRepository
	pub      events.Publisher
	log      *slog.Logger
}

// NewAdminService wires admin use cases.
func NewAdminService(users domain.UserRepository, numbers domain.NumberRepository, contacts domain.ContactRepository, tokens domain.TokenRepository, pub events.Publisher, log *slog.Logger) *AdminService {
	return &AdminService{users: users, numbers: numbers, contacts: contacts, tokens: tokens, pub: pub, log: log}
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
