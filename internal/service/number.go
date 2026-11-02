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

// BatchLookupItem is one result of a batch caller-ID lookup. Invalid phones do
// not fail the whole batch: they carry an error string instead of a profile.
type BatchLookupItem struct {
	Phone  string         `json:"phone"`
	Number *domain.Number `json:"number,omitempty"`
	Tags   []domain.Tag   `json:"tags,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// BatchLookupResult is returned by POST /numbers/lookup.
type BatchLookupResult struct {
	Results   []BatchLookupItem `json:"results"`
	Requested int               `json:"requested"`
	Returned  int               `json:"returned"`
}

// SpamCategoryInfo describes one accepted spam category.
type SpamCategoryInfo struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

// SpamCategoryCatalog is the public, ordered catalog of spam categories.
var SpamCategoryCatalog = []SpamCategoryInfo{
	{Code: "fraud", Label: "Мошенничество", Description: "Попытка получить деньги или данные обманом", Severity: "high"},
	{Code: "scam", Label: "Скам", Description: "Подозрительная схема или фишинг", Severity: "high"},
	{Code: "telemarketing", Label: "Телемаркетинг", Description: "Рекламные и продающие звонки", Severity: "medium"},
	{Code: "robocall", Label: "Робозвонок", Description: "Автоматический обзвон без человека", Severity: "medium"},
	{Code: "spam", Label: "Спам", Description: "Массовые нежелательные звонки", Severity: "low"},
	{Code: "other", Label: "Другое", Description: "Иная причина жалобы", Severity: "info"},
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
		// The counter update is analytics, not part of the answer. Running it in
		// the bounded background queue keeps the hot cache-hit path off Postgres
		// and materially lowers tail latency.
		s.emitLookup(ctx, phone, userID, "cache_hit", true)
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
	s.emitLookup(ctx, phone, userID, "cache_miss", false)
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

// BatchLookup resolves up to 100 numbers in one request. This is the endpoint
// mobile clients use to sync a call log; invalid entries are reported per item
// instead of rejecting the whole batch.
func (s *NumberService) BatchLookup(ctx context.Context, phones []string, userID *uuid.UUID) (*BatchLookupResult, error) {
	if len(phones) == 0 {
		return nil, apperr.Invalid(map[string]string{"phones": "at least one phone is required"})
	}
	if len(phones) > 100 {
		return nil, apperr.Invalid(map[string]string{"phones": "at most 100 phones per request"})
	}
	result := &BatchLookupResult{
		Results:   make([]BatchLookupItem, 0, len(phones)),
		Requested: len(phones),
	}
	for _, raw := range phones {
		raw = strings.TrimSpace(raw)
		res, err := s.Lookup(ctx, raw, userID)
		if err != nil {
			message := "lookup failed"
			if ae := apperr.From(err); ae != nil && ae.Status < 500 {
				message = ae.Message
				// Surface the per-field reason (for example "must be a valid
				// phone number") instead of the generic validation envelope.
				if fields, ok := ae.Details.(map[string]string); ok && fields["phone"] != "" {
					message = fields["phone"]
				}
			}
			result.Results = append(result.Results, BatchLookupItem{Phone: raw, Error: message})
			continue
		}
		result.Results = append(result.Results, BatchLookupItem{
			Phone:  res.Number.Phone,
			Number: &res.Number,
			Tags:   res.Tags,
		})
		result.Returned++
	}
	return result, nil
}

// Trending returns the numbers with the highest lookup counters.
func (s *NumberService) Trending(ctx context.Context, limit int) ([]domain.Number, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	numbers, err := s.numbers.TopTrending(ctx, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return numbers, nil
}

// Names returns the aggregated community name votes for a number.
func (s *NumberService) Names(ctx context.Context, phoneRaw string) ([]domain.NameCount, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	names, err := s.numbers.ListNameCounts(ctx, phone)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return names, nil
}

// AddName records or replaces the caller's name vote for a number.
func (s *NumberService) AddName(ctx context.Context, phoneRaw string, userID uuid.UUID, rawName string) (*domain.NumberName, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	name, err := platform.NormalizePersonName(rawName)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"name": err.Error()})
	}
	if err := s.ensureNumber(ctx, phone); err != nil {
		return nil, err
	}
	n := &domain.NumberName{ID: uuid.New(), Phone: phone, Name: name, SubmittedBy: &userID}
	if err := s.numbers.AddName(ctx, n); err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.numbers.RecomputeDisplayName(ctx, phone); err != nil {
		s.log.Warn("display name recompute failed", "phone", platform.MaskPhone(phone), "error", err)
	}
	s.invalidate(ctx, phone)
	metrics.NamesAdded.Inc()
	s.publish(ctx, events.NewEvent(domain.EventNumberNameAdded, phone, events.NumberNameAdded{
		NameID: n.ID, Phone: phone, Name: name, UserID: userID,
	}))
	return n, nil
}

// DeleteName removes a name vote if the caller submitted it or is an admin.
func (s *NumberService) DeleteName(ctx context.Context, phoneRaw string, nameID, actorID uuid.UUID, role domain.Role) error {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	n, err := s.numbers.GetName(ctx, nameID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("name")
		}
		return apperr.Internal(err)
	}
	if n.Phone != phone {
		return apperr.NotFound("name")
	}
	if role != domain.RoleAdmin && (n.SubmittedBy == nil || *n.SubmittedBy != actorID) {
		return apperr.Forbidden("only the name author or an admin can delete this name")
	}
	if err := s.numbers.DeleteName(ctx, nameID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("name")
		}
		return apperr.Internal(err)
	}
	if err := s.numbers.RecomputeDisplayName(ctx, phone); err != nil {
		s.log.Warn("display name recompute failed", "phone", platform.MaskPhone(phone), "error", err)
	}
	s.invalidate(ctx, phone)
	s.publish(ctx, events.NewEvent(domain.EventNumberNameRemoved, phone, events.NumberNameRemoved{
		NameID: nameID, Phone: phone, ActorID: actorID,
	}))
	return nil
}

// SpamSummary returns privacy-preserving aggregate report counts.
func (s *NumberService) SpamSummary(ctx context.Context, phoneRaw string) (*domain.SpamSummary, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number"})
	}
	summary, err := s.numbers.SpamSummary(ctx, phone)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return summary, nil
}

// SpamCategories returns the catalog accepted by POST /numbers/{phone}/spam.
func (s *NumberService) SpamCategories() []SpamCategoryInfo {
	out := make([]SpamCategoryInfo, len(SpamCategoryCatalog))
	copy(out, SpamCategoryCatalog)
	return out
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

// emitLookup publishes lookup analytics without blocking the request path. The
// optional lookup-counter increment runs in the same bounded worker so cache
// hits do not pay a synchronous Postgres write on the hot path. If the queue is
// saturated the counter is updated synchronously to avoid losing data.
func (s *NumberService) emitLookup(ctx context.Context, phone string, userID *uuid.UUID, result string, increment bool) {
	select {
	case s.lookupQ <- struct{}{}:
		go func() {
			defer func() { <-s.lookupQ }()
			workCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if increment {
				if err := s.numbers.IncrementLookups(workCtx, phone); err != nil {
					s.log.Warn("increment lookups failed", "phone", platform.MaskPhone(phone), "error", err)
				}
			}
			ev := events.NewEvent(domain.EventNumberLookedUp, phone, events.NumberLookedUp{
				Phone: phone, UserID: userID, Result: result,
			})
			if err := s.pub.Publish(workCtx, ev); err != nil {
				s.log.Debug("lookup event publish failed", "error", err)
			}
		}()
	default:
		// Analytics are best-effort, but the lookup counter is business data.
		// Under saturation update it inline rather than dropping the increment.
		if increment {
			incCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			if err := s.numbers.IncrementLookups(incCtx, phone); err != nil {
				s.log.Warn("increment lookups failed", "phone", platform.MaskPhone(phone), "error", err)
			}
			cancel()
		}
	}
}

func (s *NumberService) publish(ctx context.Context, ev domain.Event) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.pub.Publish(pubCtx, ev); err != nil {
		s.log.Warn("event publish failed", "type", ev.Type, "error", err)
	}
}
