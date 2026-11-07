// Package service implements the application use cases. It depends only on
// domain ports and small platform helpers, never on HTTP or SQL.
package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/getcontact/backend/internal/platform"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/google/uuid"
)

// Meta is the consent-based request context captured for refresh sessions.
// Device fields are optional: a client may send only User-Agent, or nothing at
// all. The API never reads photos, contacts, files or messages from a device.
type Meta struct {
	IP           string
	UserAgent    string
	DeviceID     string
	DeviceName   string
	DeviceModel  string
	Manufacturer string
	OS           string
	OSVersion    string
	AppVersion   string
	AppBuild     string
	Client       string
	Locale       string
	Timezone     string
	NetworkType  string
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

	tokens, _, err := s.issuePair(ctx, user, meta)
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
	tokens, sessionID, err := s.issuePair(ctx, user, meta)
	if err != nil {
		return nil, err
	}
	metrics.AuthAttempts.WithLabelValues("login", "ok").Inc()
	s.publish(ctx, events.NewEvent(domain.EventUserLoggedIn, user.ID.String(), events.UserLoggedIn{
		UserID: user.ID, SessionID: sessionID,
		IP: meta.IP, UserAgent: meta.UserAgent,
		DeviceID: meta.DeviceID, DeviceName: meta.DeviceName, DeviceModel: meta.DeviceModel,
		Manufacturer: meta.Manufacturer, OS: meta.OS, OSVersion: meta.OSVersion,
		AppVersion: meta.AppVersion, Client: meta.Client,
	}))
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
	// Keep the device context stable across rotation when the client does not
	// resend its headers; it belongs to the same physical session.
	meta = mergeMeta(meta, stored)
	// Invalidate any access token still tied to the rotated session.
	if err := s.cache.DenySession(ctx, stored.ID.String(), s.tokensMgr.TTL()); err != nil {
		s.log.Warn("session denylist failed", "error", err)
	}
	tokens, _, err := s.issuePair(ctx, user, meta)
	if err != nil {
		return nil, err
	}
	metrics.AuthAttempts.WithLabelValues("refresh", "ok").Inc()
	return &AuthResult{User: user.Public(), Tokens: *tokens}, nil
}

// Logout revokes the refresh token (if supplied) and blacklists the access jti
// plus the bound session.
func (s *AuthService) Logout(ctx context.Context, jti, sessionID string, accessExpiresAt time.Time, refreshToken string) error {
	if refreshToken != "" {
		if stored, err := s.tokens.GetByHash(ctx, platform.HashOpaqueToken(refreshToken)); err == nil {
			_ = s.tokens.Revoke(ctx, stored.ID)
			if err := s.cache.DenySession(ctx, stored.ID.String(), s.tokensMgr.TTL()); err != nil {
				s.log.Warn("session denylist failed", "error", err)
			}
		}
	}
	if sessionID != "" {
		if sid, err := uuid.Parse(sessionID); err == nil {
			_ = s.tokens.Revoke(ctx, sid)
		}
		if err := s.cache.DenySession(ctx, sessionID, s.tokensMgr.TTL()); err != nil {
			s.log.Warn("session denylist failed", "error", err)
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

func (s *AuthService) issuePair(ctx context.Context, user *domain.User, meta Meta) (*domain.TokenPair, uuid.UUID, error) {
	raw, hash, err := platform.NewOpaqueToken(32)
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	now := time.Now().UTC()
	refreshExp := now.Add(s.refreshTTL)
	sessionID := uuid.New()
	if err := s.tokens.Create(ctx, &domain.RefreshToken{
		ID:           sessionID,
		UserID:       user.ID,
		TokenHash:    hash,
		UserAgent:    truncate(meta.UserAgent, 256),
		IP:           truncate(meta.IP, 64),
		DeviceID:     truncate(meta.DeviceID, 128),
		DeviceName:   truncate(meta.DeviceName, 128),
		DeviceModel:  truncate(meta.DeviceModel, 128),
		Manufacturer: truncate(meta.Manufacturer, 128),
		OS:           truncate(meta.OS, 64),
		OSVersion:    truncate(meta.OSVersion, 64),
		AppVersion:   truncate(meta.AppVersion, 64),
		AppBuild:     truncate(meta.AppBuild, 64),
		Client:       truncate(meta.Client, 32),
		Locale:       truncate(meta.Locale, 32),
		Timezone:     truncate(meta.Timezone, 64),
		NetworkType:  truncate(meta.NetworkType, 32),
		LastIP:       truncate(meta.IP, 64),
		LastSeenAt:   now,
		ExpiresAt:    refreshExp,
	}); err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	access, _, accessExp, err := s.tokensMgr.IssueWithSession(user.ID, user.Role, sessionID)
	if err != nil {
		return nil, uuid.Nil, apperr.Internal(err)
	}
	return &domain.TokenPair{
		AccessToken: access, RefreshToken: raw, TokenType: "Bearer",
		ExpiresIn: int64(time.Until(accessExp).Seconds()), ExpiresAt: accessExp,
	}, sessionID, nil
}

// Sessions lists the caller's active device sessions, newest first. The current
// access token's session is flagged so a client can show "this device".
func (s *AuthService) Sessions(ctx context.Context, userID uuid.UUID, currentSessionID string) ([]domain.Session, error) {
	active, err := s.tokens.ListActiveForUser(ctx, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]domain.Session, 0, len(active))
	for i := range active {
		out = append(out, sessionView(active[i], currentSessionID))
	}
	return out, nil
}

// RevokeSession ends one of the caller's own sessions and immediately blocks
// outstanding access tokens that were issued for it.
func (s *AuthService) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID, currentSessionID string) error {
	if sessionID == uuid.Nil {
		return apperr.NotFound("session")
	}
	if err := s.tokens.RevokeForUser(ctx, sessionID, userID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("session")
		}
		return apperr.Internal(err)
	}
	// Deny remaining access tokens for the revoked session until the access-token
	// TTL has elapsed; after that the JWT expires on its own.
	if err := s.cache.DenySession(ctx, sessionID.String(), s.tokensMgr.TTL()); err != nil {
		s.log.Warn("session denylist failed", "error", err)
	}
	s.publish(ctx, events.NewEvent(domain.EventUserSessionRevoked, userID.String(), events.UserSessionRevoked{
		UserID: userID, SessionID: sessionID, ActorID: userID,
	}))
	metrics.AuthAttempts.WithLabelValues("session_revoke", "ok").Inc()
	return nil
}

func sessionView(t domain.RefreshToken, currentSessionID string) domain.Session {
	lastSeen := t.LastSeenAt
	if lastSeen.IsZero() {
		lastSeen = t.CreatedAt
	}
	return domain.Session{
		ID:           t.ID,
		Current:      t.ID.String() == currentSessionID,
		IP:           t.IP,
		LastIP:       t.LastIP,
		UserAgent:    t.UserAgent,
		DeviceID:     t.DeviceID,
		DeviceName:   t.DeviceName,
		DeviceModel:  t.DeviceModel,
		Manufacturer: t.Manufacturer,
		OS:           t.OS,
		OSVersion:    t.OSVersion,
		AppVersion:   t.AppVersion,
		AppBuild:     t.AppBuild,
		Client:       t.Client,
		Locale:       t.Locale,
		Timezone:     t.Timezone,
		NetworkType:  t.NetworkType,
		LastSeenAt:   lastSeen,
		CreatedAt:    t.CreatedAt,
		ExpiresAt:    t.ExpiresAt,
	}
}

func (s *AuthService) publish(ctx context.Context, ev domain.Event) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.pub.Publish(pubCtx, ev); err != nil {
		s.log.Warn("event publish failed", "type", ev.Type, "error", err)
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Drop control characters so header/body values cannot inject line breaks
	// into logs or audit payloads.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	// Count runes, not bytes: PostgreSQL VARCHAR(n) counts characters and a
	// byte-slice could split a multi-byte UTF-8 sequence.
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}

// mergeMeta fills any missing device field from the previous session so a
// token refresh does not erase the device context captured at login.
func mergeMeta(meta Meta, prev *domain.RefreshToken) Meta {
	if meta.IP == "" {
		meta.IP = prev.LastIP
		if meta.IP == "" {
			meta.IP = prev.IP
		}
	}
	if meta.UserAgent == "" {
		meta.UserAgent = prev.UserAgent
	}
	if meta.DeviceID == "" {
		meta.DeviceID = prev.DeviceID
	}
	if meta.DeviceName == "" {
		meta.DeviceName = prev.DeviceName
	}
	if meta.DeviceModel == "" {
		meta.DeviceModel = prev.DeviceModel
	}
	if meta.Manufacturer == "" {
		meta.Manufacturer = prev.Manufacturer
	}
	if meta.OS == "" {
		meta.OS = prev.OS
	}
	if meta.OSVersion == "" {
		meta.OSVersion = prev.OSVersion
	}
	if meta.AppVersion == "" {
		meta.AppVersion = prev.AppVersion
	}
	if meta.AppBuild == "" {
		meta.AppBuild = prev.AppBuild
	}
	if meta.Client == "" {
		meta.Client = prev.Client
	}
	if meta.Locale == "" {
		meta.Locale = prev.Locale
	}
	if meta.Timezone == "" {
		meta.Timezone = prev.Timezone
	}
	if meta.NetworkType == "" {
		meta.NetworkType = prev.NetworkType
	}
	return meta
}
