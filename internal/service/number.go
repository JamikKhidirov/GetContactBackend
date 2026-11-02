package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/getcontact/backend/internal/platform"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/google/uuid"
)

const numberCachePrefix = "gc:number:v1:"

// LookupResult is the caller-ID card plus community tags.
type LookupResult struct {
	Number domain.Number `json:"number"`
	Tags   []domain.Tag  `json:"tags"`
}

// NumberService implements caller-ID lookup, community tags and spam reports.
type NumberService struct {
	numbers domain.NumberRepository
	cache   *rediscache.Cache
	pub     events.Publisher
	log     *slog.Logger
	ttl     time.Duration
	lookupQ chan struct{}
}

// NewNumberService wires caller-ID use cases.
func NewNumberService(numbers domain.NumberRepository, cache *rediscache.Cache, pub events.Publisher, log *slog.Logger, ttl time.Duration) *NumberService {
	return &NumberService{numbers: numbers, cache: cache, pub: pub, log: log, ttl: ttl,
		lookupQ: make(chan struct{}, 64)}
}

// Lookup returns the cached or persisted profile for a phone number. Unknown
// numbers get a zero-score profile so the client always receives a card.
func (s *NumberService) Lookup(ctx context.Context, phoneRaw string, userID *uuid.UUID) (*LookupResult, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number (8..15 digits)"})
	}
	key := numberCachePrefix + phone
	var cached LookupResult
	if ok, cacheErr := s.cache.GetJSON(ctx, key, &cached); cacheErr == nil && ok {
		metrics.NumberLookups.WithLabelValues("cache_hit").Inc()
		_ = s.numbers.IncrementLookups(ctx, phone)
		s.emitLookup(phone, userID, "cache_hit")
		return &cached, nil
	}

	n, err := s.numbers.Get(ctx, phone)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			metrics.NumberLookups.WithLabelValues("error").Inc()
			return nil, apperr.Internal(err)
		}
		n = &domain.Number{Phone: phone}
		if err := s.numbers.Upsert(ctx, n); err != nil {
			return nil, apperr.Internal(err)
		}
	}
	tags, err := s.numbers.ListTags(ctx, phone)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.numbers.IncrementLookups(ctx, phone); err != nil {
		s.log.Warn("increment lookups failed", "phone", platform.MaskPhone(phone), "error", err)
	}
	n.LookupsCount++

	result := &LookupResult{Number: *n, Tags: tags}
	if err := s.cache.SetJSON(ctx, key, result, s.ttl); err != nil {
		s.log.Debug("number cache write failed", "error", err)
	}
	metrics.NumberLookups.WithLabelValues("cache_miss").Inc()
	s.emitLookup(phone, userID, "cache_miss")
	return result, nil
}

// AddTag applies a community tag (upserting weight) and refreshes the score.
func (s *NumberService) AddTag(ctx context.Context, phoneRaw string, userID uuid.UUID, tagRaw string) (*domain.Tag, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	tagName, err := platform.NormalizeTag(tagRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"tag": err.Error()})
	}
	if err := s.ensureNumber(ctx, phone); err != nil {
		return nil, err
	}
	t := &domain.Tag{ID: uuid.New(), Phone: phone, Tag: tagName, CreatedBy: &userID}
	if err := s.numbers.AddTag(ctx, t); err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.numbers.RecomputeSpamScore(ctx, phone); err != nil {
		s.log.Warn("spam score recompute failed", "phone", platform.MaskPhone(phone), "error", err)
	}
	s.invalidate(ctx, phone)
	metrics.TagsAdded.Inc()
	s.publish(ctx, events.NewEvent(domain.EventTagAdded, phone, events.TagAdded{
		Phone: phone, Tag: tagName, Weight: t.Weight, UserID: userID,
	}))
	return t, nil
}

// DeleteTag removes a tag if the caller created it or is an admin.
func (s *NumberService) DeleteTag(ctx context.Context, phoneRaw string, tagID, actorID uuid.UUID, role domain.Role) error {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	t, err := s.numbers.GetTag(ctx, tagID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("tag")
		}
		return apperr.Internal(err)
	}
	if t.Phone != phone {
		return apperr.NotFound("tag")
	}
	if role != domain.RoleAdmin && (t.CreatedBy == nil || *t.CreatedBy != actorID) {
		return apperr.Forbidden("only the tag author or an admin can delete this tag")
	}
	if err := s.numbers.DeleteTag(ctx, tagID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("tag")
		}
		return apperr.Internal(err)
	}
	_ = s.numbers.RecomputeSpamScore(ctx, phone)
	s.invalidate(ctx, phone)
	return nil
}

// ReportSpam records one report per user per number and returns the new card.
func (s *NumberService) ReportSpam(ctx context.Context, phoneRaw string, reporterID uuid.UUID, category, comment string) (*LookupResult, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	category = strings.ToLower(strings.TrimSpace(category))
	comment = strings.TrimSpace(comment)
	fields := map[string]string{}
	if err := platform.ValidateSpamCategory(category); err != nil {
		fields["category"] = err.Error()
	}
	if err := platform.ValidateComment(comment); err != nil {
		fields["comment"] = err.Error()
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid(fields)
	}
	if err := s.ensureNumber(ctx, phone); err != nil {
		return nil, err
	}
	rep := &domain.SpamReport{
		ID: uuid.New(), Phone: phone, ReporterID: reporterID,
		Category: category, Comment: comment,
	}
	if err := s.numbers.AddSpamReport(ctx, rep); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, apperr.Conflict("you have already reported this number")
		}
		return nil, apperr.Internal(err)
	}
	if err := s.numbers.RecomputeSpamScore(ctx, phone); err != nil {
		s.log.Warn("spam score recompute failed", "phone", platform.MaskPhone(phone), "error", err)
	}
	s.invalidate(ctx, phone)
	metrics.SpamReports.Inc()
	s.publish(ctx, events.NewEvent(domain.EventSpamReported, phone, events.SpamReported{
		ReportID: rep.ID, Phone: phone, Category: category, ReporterID: reporterID,
	}))

	n, err := s.numbers.Get(ctx, phone)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	tags, err := s.numbers.ListTags(ctx, phone)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &LookupResult{Number: *n, Tags: tags}, nil
}

// Tags returns the community tag catalog ordered by popularity.
func (s *NumberService) Tags(ctx context.Context, limit int) ([]domain.TagCount, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tags, err := s.numbers.TopTags(ctx, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return tags, nil
}

// ListTags returns tags for one number.
func (s *NumberService) ListTags(ctx context.Context, phoneRaw string) ([]domain.Tag, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	tags, err := s.numbers.ListTags(ctx, phone)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return tags, nil
}

// TopSpam returns the most reported numbers.
func (s *NumberService) TopSpam(ctx context.Context, limit int) ([]domain.Number, error) {
	nums, err := s.numbers.TopSpam(ctx, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return nums, nil
}

func (s *NumberService) ensureNumber(ctx context.Context, phone string) error {
	if _, err := s.numbers.Get(ctx, phone); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return apperr.Internal(err)
		}
		if err := s.numbers.Upsert(ctx, &domain.Number{Phone: phone}); err != nil {
			return apperr.Internal(err)
		}
	}
	return nil
}

func (s *NumberService) invalidate(ctx context.Context, phone string) {
	s.cache.Delete(ctx, numberCachePrefix+phone)
}

// emitLookup publishes lookup analytics without blocking the request path.
func (s *NumberService) emitLookup(phone string, userID *uuid.UUID, result string) {
	select {
	case s.lookupQ <- struct{}{}:
		go func() {
			defer func() { <-s.lookupQ }()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			ev := events.NewEvent(domain.EventNumberLookedUp, phone, events.NumberLookedUp{
				Phone: phone, UserID: userID, Result: result,
			})
			if err := s.pub.Publish(ctx, ev); err != nil {
				s.log.Debug("lookup event publish failed", "error", err)
			}
		}()
	default:
		// analytics are best-effort; never queue unbounded work
	}
}

func (s *NumberService) publish(ctx context.Context, ev domain.Event) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.pub.Publish(pubCtx, ev); err != nil {
		s.log.Warn("event publish failed", "type", ev.Type, "error", err)
	}
}
