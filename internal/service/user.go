package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/platform"
	"github.com/google/uuid"
)

// UserService implements profile self-service.
type UserService struct {
	users  domain.UserRepository
	tokens domain.TokenRepository
	argon  platform.ArgonParams
	log    *slog.Logger
}

// NewUserService wires profile use cases.
func NewUserService(users domain.UserRepository, tokens domain.TokenRepository, argon platform.ArgonParams, log *slog.Logger) *UserService {
	return &UserService{users: users, tokens: tokens, argon: argon, log: log}
}

// UpdateMeInput uses pointers so PATCH can distinguish "absent" from "empty".
type UpdateMeInput struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
}

// Get returns a public profile.
func (s *UserService) Get(ctx context.Context, id uuid.UUID) (*domain.User, error) {
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

// UpdateMe applies a partial profile update.
func (s *UserService) UpdateMe(ctx context.Context, userID uuid.UUID, in UpdateMeInput) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperr.NotFound("user")
		}
		return nil, apperr.Internal(err)
	}
	fields := map[string]string{}
	if in.DisplayName != nil {
		if err := platform.ValidateDisplayName(*in.DisplayName); err != nil {
			fields["display_name"] = err.Error()
		} else {
			u.DisplayName = *in.DisplayName
		}
	}
	if in.Email != nil {
		if err := platform.ValidateEmail(*in.Email); err != nil {
			fields["email"] = err.Error()
		} else {
			u.Email = *in.Email
		}
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid(fields)
	}
	if err := s.users.Update(ctx, u); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return nil, apperr.Conflict("email is already in use")
		}
		return nil, apperr.Internal(err)
	}
	u.PasswordHash = ""
	return u, nil
}

// ChangePassword verifies the current password, stores a new hash and revokes
// every active refresh session.
func (s *UserService) ChangePassword(ctx context.Context, userID uuid.UUID, current, next string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("user")
		}
		return apperr.Internal(err)
	}
	if !platform.VerifyPassword(current, u.PasswordHash) {
		return apperr.Unauthorized("current password is incorrect")
	}
	if err := platform.ValidatePassword(next); err != nil {
		return apperr.Invalid(map[string]string{"new_password": err.Error()})
	}
	hash, err := platform.HashPassword(next, s.argon)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.users.UpdatePassword(ctx, u.ID, hash); err != nil {
		return apperr.Internal(err)
	}
	if err := s.tokens.RevokeAllForUser(ctx, u.ID); err != nil {
		s.log.Warn("failed to revoke sessions after password change", "user_id", u.ID, "error", err)
	}
	return nil
}

// DeleteMe permanently deletes the account (contacts and sessions cascade).
func (s *UserService) DeleteMe(ctx context.Context, userID uuid.UUID) error {
	if err := s.users.Delete(ctx, userID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return apperr.NotFound("user")
		}
		return apperr.Internal(err)
	}
	return nil
}
