// Package service implements the application use cases. It depends only on
// domain ports and small platform helpers, never on HTTP or SQL.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/getcontact/backend/internal/platform"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/google/uuid"
)

// Meta is request metadata captured for refresh-token sessions.
type Meta struct {
	IP        string
	UserAgent string
}

// AuthResult is returned by register/login/refresh.
type AuthResult struct {
	User   domain.User      `json:"user"`
	Tokens domain.TokenPair `json:"tokens"`
}

// AuthService owns registration, login, refresh rotation and logout.
type AuthService struct {
	users      domain.UserRepository
	tokens     domain.TokenRepository
	cache      *rediscache.Cache
	tokensMgr  *platform.TokenManager
	refreshTTL time.Duration
	argon      platform.ArgonParams
	pub        events.Publisher
	log        *slog.Logger
}

// NewAuthService wires the authentication use cases.
func NewAuthService(
	users domain.UserRepository,
	tokens domain.TokenRepository,
	cache *rediscache.Cache,
	tm *platform.TokenManager,
	refreshTTL time.Duration,
	argon platform.ArgonParams,
	pub events.Publisher,
	log *slog.Logger,
) *AuthService {
	return &AuthService{users: users, tokens: tokens, cache: cache, tokensMgr: tm,
		refreshTTL: refreshTTL, argon: argon, pub: pub, log: log}
}

// RegisterInput is the registration command.
type RegisterInput struct {
	Phone       string
	Password    string
	DisplayName string
	Email       string
}

// Register creates a user and returns the first token pair.
func (s *AuthService) Register(ctx context.Context, in RegisterInput, meta Meta) (*AuthResult, error) {
	phone, err := platform.NormalizePhone(in.Phone)
	if err != nil {
		metrics.AuthAttempts.WithLabelValues("register", "invalid").Inc()
		return nil, apperr.Invalid(map[string]string{"phone": "must be a valid phone number (8..15 digits)"})
	}
	fields := map[string]string{}
	if err := platform.ValidatePassword(in.Password); err != nil {
		fields["password"] = err.Error()
	}
	if err := platform.ValidateDisplayName(in.DisplayName); err != nil {
		fields["display_name"] = err.Error()
	}
	if err := platform.ValidateEmail(in.Email); err != nil {
		fields["email"] = err.Error()
	}
	if len(fields) > 0 {
		metrics.AuthAttempts.WithLabelValues("register", "invalid").Inc()
		return nil, apperr.Invalid(fields)
	}

	hash, err := platform.HashPassword(in.Password, s.argon)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	user := &domain.User{
		ID:           uuid.New(),
		Phone:        phone,
		Email:        in.Email,
		PasswordHash: hash,
		DisplayName:  in.DisplayName,
		Role:         domain.RoleUser,
	}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			metrics.AuthAttempts.WithLabelValues("register", "conflict").Inc()
			return nil, apperr.Conflict("user with this phone or email already exists")
		}
		return nil, apperr.Internal(err)
	}

	tokens, err := s.issuePair(ctx, user, meta)
	if err != nil {
		return nil, err
	}
	metrics.AuthAttempts.WithLabelValues("register", "ok").Inc()
	s.publish(ctx, events.NewEvent(domain.EventUserRegistered, user.ID.String(), events.UserRegistered{
		UserID: user.ID, Phone: user.Phone, DisplayName: user.DisplayName,
	}))
	return &AuthResult{User: user.Public(), Tokens: *tokens}, nil
}

// Login verifies credentials and issues a token pair.
func (s *AuthService) Login(ctx context.Context, phoneRaw, password string, meta Meta) (*AuthResult, error) {
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		metrics.AuthAttempts.WithLabelValues("login", "invalid").Inc()
		return nil, apperr.Unauthorized("invalid credentials")
	}
	user, err := s.users.GetByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			metrics.AuthAttempts.WithLabelValues("login", "denied").Inc()
			// Equalise timing with the success path.
			_ = platform.VerifyPassword(password, "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
			return nil, apperr.Unauthorized("invalid credentials")
		}
		return nil, apperr.Internal(err)
	}
	if !platform.VerifyPassword(password, user.PasswordHash) {
		metrics.AuthAttempts.WithLabelValues("login", "denied").Inc()
		return nil, apperr.Unauthorized("invalid credentials")
	}
	if user.IsBlocked {
		metrics.AuthAttempts.WithLabelValues("login", "blocked").Inc()
		return nil, apperr.Forbidden("user is blocked")
	}
	tokens, err := s.issuePair(ctx, user, meta)
	if err != nil {
		return nil, err
	}
	metrics.AuthAttempts.WithLabelValues("login", "ok").Inc()
	return &AuthResult{User: user.Public(), Tokens: *tokens}, nil
}

// Refresh rotates a refresh token (single use) and returns a fresh pair.
func (s *AuthService) Refresh(ctx context.Context, rawToken string, meta Meta) (*AuthResult, error) {
	if rawToken == "" {
		return nil, apperr.Unauthorized("refresh_token is required")
	}
	hash := platform.HashOpaqueToken(rawToken)
	stored, err := s.tokens.GetByHash(ctx, hash)
	if err != nil {
		metrics.AuthAttempts.WithLabelValues("refresh", "denied").Inc()
		return nil, apperr.Unauthorized("invalid or expired refresh token")
	}
	if err := s.tokens.Revoke(ctx, stored.ID); err != nil {
		return nil, apperr.Internal(err)
	}
	user, err := s.users.GetByID(ctx, stored.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.Unauthorized("user no longer exists")
		}
		return nil, apperr.Internal(err)
	}
	if user.IsBlocked {
		return nil, apperr.Forbidden("user is blocked")
	}
	tokens, err := s.issuePair(ctx, user, meta)
	if err != nil {
		return nil, err
	}
	metrics.AuthAttempts.WithLabelValues("refresh", "ok").Inc()
	return &AuthResult{User: user.Public(), Tokens: *tokens}, nil
}

// Logout revokes the refresh token (if supplied) and blacklists the access jti.
func (s *AuthService) Logout(ctx context.Context, jti string, accessExpiresAt time.Time, refreshToken string) error {
	if refreshToken != "" {
		if stored, err := s.tokens.GetByHash(ctx, platform.HashOpaqueToken(refreshToken)); err == nil {
			_ = s.tokens.Revoke(ctx, stored.ID)
		}
	}
	if jti != "" {
		ttl := time.Until(accessExpiresAt)
		if ttl > 0 {
			if err := s.cache.DenyAccessToken(ctx, jti, ttl); err != nil {
				s.log.Warn("access token denylist failed", "error", err)
			}
		}
	}
	metrics.AuthAttempts.WithLabelValues("logout", "ok").Inc()
	return nil
}

// Me returns the authenticated user's profile.
func (s *AuthService) Me(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("user")
		}
		return nil, apperr.Internal(err)
	}
	if user.IsBlocked {
		return nil, apperr.Forbidden("user is blocked")
	}
	return user, nil
}

// BootstrapAdmin creates the initial admin when configured and missing.
func (s *AuthService) BootstrapAdmin(ctx context.Context, phoneRaw, password, name string) error {
	if phoneRaw == "" || password == "" {
		return nil
	}
	phone, err := platform.NormalizePhone(phoneRaw)
	if err != nil {
		return err
	}
	hash, err := platform.HashPassword(password, s.argon)
	if err != nil {
		return err
	}
	existing, err := s.users.GetByPhone(ctx, phone)
	if err == nil {
		if existing.Role != domain.RoleAdmin {
			if err := s.users.SetRole(ctx, existing.ID, domain.RoleAdmin); err != nil {
				return err
			}
		}
		// Keep the bootstrap credentials aligned with the configured env so a
		// demo stack with a persisted volume can always be reproduced.
		return s.users.UpdatePassword(ctx, existing.ID, hash)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return s.users.Create(ctx, &domain.User{
		ID: uuid.New(), Phone: phone, PasswordHash: hash,
		DisplayName: name, Role: domain.RoleAdmin,
	})
}

func (s *AuthService) issuePair(ctx context.Context, user *domain.User, meta Meta) (*domain.TokenPair, error) {
	access, jti, accessExp, err := s.tokensMgr.Issue(user.ID, user.Role)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	_ = jti
	raw, hash, err := platform.NewOpaqueToken(32)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	refreshExp := time.Now().UTC().Add(s.refreshTTL)
	if err := s.tokens.Create(ctx, &domain.RefreshToken{
		ID: uuid.New(), UserID: user.ID, TokenHash: hash,
		UserAgent: truncate(meta.UserAgent, 256), IP: truncate(meta.IP, 64), ExpiresAt: refreshExp,
	}); err != nil {
		return nil, apperr.Internal(err)
	}
	return &domain.TokenPair{
		AccessToken: access, RefreshToken: raw, TokenType: "Bearer",
		ExpiresIn: int64(time.Until(accessExp).Seconds()), ExpiresAt: accessExp,
	}, nil
}

func (s *AuthService) publish(ctx context.Context, ev domain.Event) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.pub.Publish(pubCtx, ev); err != nil {
		s.log.Warn("event publish failed", "type", ev.Type, "error", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
