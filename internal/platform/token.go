package platform

import (
	"errors"
	"fmt"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the access-token payload.
type Claims struct {
	Role string `json:"role"`
	Type string `json:"typ"`
	jwt.RegisteredClaims
}

// TokenManager issues and verifies short-lived JWT access tokens.
type TokenManager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

// NewTokenManager builds a TokenManager. The secret must be non-empty.
func NewTokenManager(secret, issuer string, ttl time.Duration) (*TokenManager, error) {
	if len(secret) == 0 {
		return nil, errors.New("jwt secret is required")
	}
	return &TokenManager{secret: []byte(secret), issuer: issuer, ttl: ttl}, nil
}

// TTL exposes the access-token lifetime.
func (m *TokenManager) TTL() time.Duration { return m.ttl }

// Issue creates a signed HS256 access token and returns it with the jti and expiry.
func (m *TokenManager) Issue(userID uuid.UUID, role domain.Role) (token, jti string, expiresAt time.Time, err error) {
	now := time.Now().UTC()
	expiresAt = now.Add(m.ttl)
	jti = uuid.NewString()
	claims := Claims{
		Role: string(role),
		Type: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID.String(),
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return token, jti, expiresAt, err
}

// Parse validates the signature, algorithm, issuer and expiry of an access token.
func (m *TokenManager) Parse(raw string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(m.issuer))
	if err != nil || !token.Valid {
		return nil, domain.ErrInvalidToken
	}
	if claims.Type != "access" {
		return nil, domain.ErrInvalidToken
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return nil, domain.ErrInvalidToken
	}
	return claims, nil
}

// UserID returns the parsed subject of validated claims.
func (c *Claims) UserID() uuid.UUID {
	id, _ := uuid.Parse(c.Subject)
	return id
}
