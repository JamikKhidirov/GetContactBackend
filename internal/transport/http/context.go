package httptransport

import (
	"context"
	"time"

	"github.com/getcontact/backend/internal/domain"
	"github.com/google/uuid"
)

type contextKey int

const (
	ctxUserID contextKey = iota
	ctxRole
	ctxTokenID
	ctxTokenExpiry
)

func withClaims(ctx context.Context, userID uuid.UUID, role domain.Role, jti string, exp time.Time) context.Context {
	ctx = context.WithValue(ctx, ctxUserID, userID)
	ctx = context.WithValue(ctx, ctxRole, role)
	ctx = context.WithValue(ctx, ctxTokenID, jti)
	ctx = context.WithValue(ctx, ctxTokenExpiry, exp)
	return ctx
}

func userIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxUserID).(uuid.UUID)
	return id, ok
}

func roleFrom(ctx context.Context) domain.Role {
	role, _ := ctx.Value(ctxRole).(domain.Role)
	return role
}

func tokenIDFrom(ctx context.Context) string {
	jti, _ := ctx.Value(ctxTokenID).(string)
	return jti
}

func tokenExpiryFrom(ctx context.Context) time.Time {
	exp, _ := ctx.Value(ctxTokenExpiry).(time.Time)
	return exp
}
