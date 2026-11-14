package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/getcontact/backend/internal/platform"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/google/uuid"
)

// Email purposes accepted by the public API.
const (
	EmailPurposeSignup      = "signup"
	EmailPurposeLogin       = "login"
	EmailPurposeVerifyEmail = "verify_email"
)

// RequestCodeInput asks for a one-time code to be emailed.
type RequestCodeInput struct {
	Email   string
	Purpose string
	UserID  *uuid.UUID
	Meta    Meta
}

// EmailCodeResult is intentionally uniform: it does not reveal whether an
// address belongs to an account.
type EmailCodeResult struct {
	Sent        bool      `json:"sent"`
	Email       string    `json:"email"`
	Purpose     string    `json:"purpose"`
	ExpiresAt   time.Time `json:"expires_at"`
	ExpiresIn   int64     `json:"expires_in"`
	ResendAfter int       `json:"resend_after_seconds"`
	// DevCode is only populated in development with the log mail provider. It
	// is never returned in production.
	DevCode string `json:"dev_code,omitempty"`
}

// EmailAuthService implements passwordless email login and address ownership
// verification on top of the transactional outbox.
type EmailAuthService struct {
	cfg   config.EmailConfig
	env   string
	users domain.UserRepository
	repo  domain.EmailRepository
	auth  *AuthService
	cache *rediscache.Cache
	log   *slog.Logger
}

// NewEmailAuthService wires the email authentication use cases.
func NewEmailAuthService(
	cfg config.EmailConfig,
	env string,
	users domain.UserRepository,
	repo domain.EmailRepository,
	auth *AuthService,
	cache *rediscache.Cache,
	log *slog.Logger,
) *EmailAuthService {
	return &EmailAuthService{cfg: cfg, env: env, users: users, repo: repo, auth: auth, cache: cache, log: log}
}

// RequestCode creates a code and writes the delivery event atomically. Even if
// the API crashes immediately after returning, the outbox relay will still
// publish the event and the worker will send the email.
func (s *EmailAuthService) RequestCode(ctx context.Context, in RequestCodeInput) (*EmailCodeResult, error) {
	if !s.cfg.Enabled {
		return nil, apperr.New(503, "EMAIL_DISABLED", "email authentication is disabled")
	}
	email, err := platform.NormalizeEmail(in.Email)
	if err != nil {
		metrics.EmailRequests.WithLabelValues(in.Purpose, "invalid").Inc()
		return nil, apperr.Invalid(map[string]string{"email": err.Error()})
	}
	purpose := strings.ToLower(strings.TrimSpace(in.Purpose))
	switch purpose {
	case EmailPurposeSignup, EmailPurposeLogin, EmailPurposeVerifyEmail:
	default:
		metrics.EmailRequests.WithLabelValues(purpose, "invalid").Inc()
		return nil, apperr.Invalid(map[string]string{"purpose": "must be signup, login or verify_email"})
	}

	// A cross-instance cooldown keyed by address and purpose applies to every
	// address, including ones without an account. Without it, an attacker could
	// probe unlimited unknown addresses and use response timing/email receipt as
	// an account-existence oracle.
	if s.cache != nil && s.cfg.Cooldown > 0 {
		key := "gc:email:cooldown:" + purpose + ":" + email
		ok, err := s.cache.SetNX(ctx, key, 1, s.cfg.Cooldown)
		if err != nil {
			s.log.Warn("email cooldown unavailable", "error", err)
		} else if !ok {
			metrics.EmailRequests.WithLabelValues(purpose, "cooldown").Inc()
			return nil, apperr.TooManyRequests()
		}
	}

	var userID *uuid.UUID
	switch purpose {
	case EmailPurposeVerifyEmail:
		if in.UserID == nil {
			return nil, apperr.Unauthorized("authentication required to verify an email")
		}
		user, err := s.users.GetByID(ctx, *in.UserID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, apperr.Unauthorized("user no longer exists")
			}
			return nil, apperr.Internal(err)
		}
		id := user.ID
		userID = &id
	case EmailPurposeLogin:
		user, err := s.users.GetByEmail(ctx, email)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.Internal(err)
		}
		if user != nil {
			id := user.ID
			userID = &id
		}
	}

	// Accept the request silently for unknown addresses, but still enforce the
	// cooldown for addresses that do exist.
	last, err := s.repo.LastRequestedAt(ctx, email, purpose)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !last.IsZero() && time.Since(last) < s.cfg.Cooldown {
		metrics.EmailRequests.WithLabelValues(purpose, "cooldown").Inc()
		return nil, apperr.TooManyRequests()
	}

	result := &EmailCodeResult{
		Sent: true, Email: email, Purpose: purpose,
		ExpiresAt:   time.Now().UTC().Add(s.cfg.CodeTTL),
		ExpiresIn:   int64(s.cfg.CodeTTL.Seconds()),
		ResendAfter: int(s.cfg.Cooldown.Seconds()),
	}
	if purpose == EmailPurposeLogin && userID == nil {
		// Do not create a verification for an address that has no account and
		// never reveal that fact to the caller.
		metrics.EmailRequests.WithLabelValues(purpose, "ok").Inc()
		return result, nil
	}

	code, err := platform.GenerateNumericCode(s.cfg.CodeLength)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	ciphertext, err := platform.EncryptEmailCode(s.cfg.Secret, code)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	now := time.Now().UTC()
	v := &domain.EmailVerification{
		ID:             uuid.New(),
		Email:          email,
		UserID:         userID,
		Purpose:        purpose,
		CodeHash:       platform.HashEmailCode(s.cfg.Secret, email, purpose, code),
		CodeCiphertext: ciphertext,
		MaxAttempts:    s.cfg.MaxAttempts,
		RequestIP:      truncate(in.Meta.IP, 64),
		UserAgent:      truncate(in.Meta.UserAgent, 256),
		ExpiresAt:      now.Add(s.cfg.CodeTTL),
		CreatedAt:      now,
	}
	ev := events.NewEvent(domain.EventEmailCodeRequested, v.ID.String(), events.EmailCodeRequested{
		VerificationID: v.ID, Email: email, Purpose: purpose, ExpiresAt: v.ExpiresAt,
	})
	outbox, err := outboxFor(ev)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := s.repo.CreateVerification(ctx, v, outbox); err != nil {
		return nil, apperr.Internal(err)
	}
	result.ExpiresAt = v.ExpiresAt

	if s.cfg.DevEcho && strings.EqualFold(s.env, "development") {
		result.DevCode = code
	}
	metrics.EmailRequests.WithLabelValues(purpose, "ok").Inc()
	s.log.Info("email code queued", "verification_id", v.ID, "email", platform.MaskEmail(email), "purpose", purpose)
	return result, nil
}

// LoginWithCode exchanges a valid one-time code for a token pair.
func (s *EmailAuthService) LoginWithCode(ctx context.Context, email, code string, meta Meta) (*AuthResult, error) {
	normalized, err := platform.NormalizeEmail(email)
	if err != nil {
		metrics.EmailVerifyAttempts.WithLabelValues(EmailPurposeLogin, "invalid").Inc()
		return nil, apperr.Unauthorized("invalid or expired code")
	}
	if _, err := s.consumeCode(ctx, normalized, EmailPurposeLogin, code); err != nil {
		return nil, err
	}
	user, err := s.users.GetByEmail(ctx, normalized)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.Unauthorized("invalid or expired code")
		}
		return nil, apperr.Internal(err)
	}
	if user.IsBlocked {
		metrics.EmailVerifyAttempts.WithLabelValues(EmailPurposeLogin, "blocked").Inc()
		return nil, apperr.Blocked()
	}
	if !user.EmailVerified {
		// A successful email login is itself proof of ownership.
		if err := s.repo.SetEmailVerified(ctx, user.ID, normalized); err != nil {
			s.log.Warn("mark email verified after login", "user_id", user.ID, "error", err)
		} else {
			user.EmailVerified = true
		}
	}
	tokens, sessionID, err := s.auth.issuePair(ctx, user, meta)
	if err != nil {
		return nil, err
	}
	s.auth.publish(ctx, events.NewEvent(domain.EventUserEmailLogin, user.ID.String(), events.UserEmailLogin{
		UserID: user.ID, SessionID: sessionID, Email: normalized,
		IP: meta.IP, UserAgent: meta.UserAgent, DeviceID: meta.DeviceID, Client: meta.Client,
	}))
	metrics.EmailVerifyAttempts.WithLabelValues(EmailPurposeLogin, "ok").Inc()
	metrics.AuthAttempts.WithLabelValues("email_login", "ok").Inc()
	return &AuthResult{User: user.Public(), Tokens: *tokens}, nil
}

// ConfirmEmailVerification proves ownership and binds the address to the user.
func (s *EmailAuthService) ConfirmEmailVerification(ctx context.Context, userID uuid.UUID, email, code string) (*domain.User, error) {
	normalized, err := platform.NormalizeEmail(email)
	if err != nil {
		metrics.EmailVerifyAttempts.WithLabelValues(EmailPurposeVerifyEmail, "invalid").Inc()
		return nil, apperr.Invalid(map[string]string{"email": err.Error()})
	}
	if _, err := s.consumeCode(ctx, normalized, EmailPurposeVerifyEmail, code); err != nil {
		return nil, err
	}
	if err := s.repo.SetEmailVerified(ctx, userID, normalized); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			metrics.EmailVerifyAttempts.WithLabelValues(EmailPurposeVerifyEmail, "conflict").Inc()
			return nil, apperr.Conflict("this email is already linked to another account")
		}
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.Unauthorized("user no longer exists")
		}
		return nil, apperr.Internal(err)
	}
	s.auth.publish(ctx, events.NewEvent(domain.EventUserEmailVerified, userID.String(), events.UserEmailVerified{
		UserID: userID, Email: normalized,
	}))
	metrics.EmailVerifyAttempts.WithLabelValues(EmailPurposeVerifyEmail, "ok").Inc()
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	public := user.Public()
	return &public, nil
}

// ConfirmSignupCode checks and burns a signup code. Registration calls it
// before creating the account so an invalid code cannot create a user.
func (s *EmailAuthService) ConfirmSignupCode(ctx context.Context, email, code string) error {
	normalized, err := platform.NormalizeEmail(email)
	if err != nil {
		return apperr.Invalid(map[string]string{"email": err.Error()})
	}
	if _, err := s.consumeCode(ctx, normalized, EmailPurposeSignup, code); err != nil {
		return err
	}
	metrics.EmailVerifyAttempts.WithLabelValues(EmailPurposeSignup, "ok").Inc()
	return nil
}

// VerifyCode checks a code without consuming it. It is exported for tests and
// for flows that need to validate first and persist later.
func (s *EmailAuthService) VerifyCode(ctx context.Context, email, purpose, code string) error {
	_, err := s.findAndCheckCode(ctx, email, purpose, code)
	return err
}

func (s *EmailAuthService) consumeCode(ctx context.Context, email, purpose, code string) (*domain.EmailVerification, error) {
	v, err := s.findAndCheckCode(ctx, email, purpose, code)
	if err != nil {
		return nil, err
	}
	ok, err := s.repo.ConsumeVerification(ctx, v.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !ok {
		metrics.EmailVerifyAttempts.WithLabelValues(purpose, "expired").Inc()
		return nil, apperr.Unauthorized("invalid or expired code")
	}
	return v, nil
}

func (s *EmailAuthService) findAndCheckCode(ctx context.Context, email, purpose, code string) (*domain.EmailVerification, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		metrics.EmailVerifyAttempts.WithLabelValues(purpose, "invalid").Inc()
		return nil, apperr.Invalid(map[string]string{"code": "code is required"})
	}
	v, err := s.repo.LatestVerification(ctx, email, purpose)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			metrics.EmailVerifyAttempts.WithLabelValues(purpose, "not_found").Inc()
			return nil, apperr.Unauthorized("invalid or expired code")
		}
		return nil, apperr.Internal(err)
	}
	if v.Attempts >= v.MaxAttempts {
		metrics.EmailVerifyAttempts.WithLabelValues(purpose, "locked").Inc()
		return nil, apperr.Unauthorized("too many incorrect attempts, request a new code")
	}
	if !platform.VerifyEmailCode(s.cfg.Secret, email, purpose, code, v.CodeHash) {
		attempts, err := s.repo.IncrementAttempts(ctx, v.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.log.Warn("increment email attempts failed", "verification_id", v.ID, "error", err)
		}
		if attempts >= v.MaxAttempts {
			_, _ = s.repo.ConsumeVerification(ctx, v.ID)
		}
		metrics.EmailVerifyAttempts.WithLabelValues(purpose, "denied").Inc()
		return nil, apperr.Unauthorized("invalid or expired code")
	}
	return v, nil
}

// outboxFor converts a domain event into a transactional outbox row.
func outboxFor(ev domain.Event) (*domain.OutboxMessage, error) {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return nil, err
	}
	version := ev.Version
	if version == 0 {
		version = 1
	}
	return &domain.OutboxMessage{
		EventID: ev.ID, EventType: ev.Type, EventVersion: version,
		AggregateID: ev.AggregateID, Payload: payload, OccurredAt: ev.OccurredAt,
	}, nil
}
