package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/getcontact/backend/internal/platform"
	"github.com/google/uuid"
)

// EmailDeliveryService turns a queued verification into a rendered email and
// hands it to the configured provider. It runs inside the Kafka worker.
type EmailDeliveryService struct {
	cfg    config.EmailConfig
	repo   domain.EmailRepository
	mailer platform.Mailer
	log    *slog.Logger
}

// NewEmailDeliveryService wires the worker-side email delivery use case.
func NewEmailDeliveryService(cfg config.EmailConfig, repo domain.EmailRepository, mailer platform.Mailer, log *slog.Logger) *EmailDeliveryService {
	return &EmailDeliveryService{cfg: cfg, repo: repo, mailer: mailer, log: log}
}

// DeliverCode loads the encrypted code, renders the HTML/text email and sends
// it with bounded retries. The operation is at-least-once: a crash after send
// but before the delivered marker causes a redelivery, never a lost email.
func (s *EmailDeliveryService) DeliverCode(ctx context.Context, verificationID uuid.UUID) error {
	v, err := s.repo.GetVerification(ctx, verificationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.log.Warn("email verification not found, dropping delivery event", "verification_id", verificationID)
			return nil
		}
		return fmt.Errorf("load verification: %w", err)
	}
	if v.DeliveredAt != nil {
		s.log.Debug("email already delivered", "verification_id", v.ID)
		return nil
	}
	if time.Now().After(v.ExpiresAt) {
		s.fail(ctx, v, "code expired before delivery")
		return nil
	}

	code, err := platform.DecryptEmailCode(s.cfg.Secret, v.CodeCiphertext)
	if err != nil {
		s.fail(ctx, v, "decrypt code: "+err.Error())
		return fmt.Errorf("decrypt code: %w", err)
	}
	ttlMinutes := int(time.Until(v.ExpiresAt).Minutes())
	if ttlMinutes < 1 {
		ttlMinutes = 1
	}
	subject, html, text, err := platform.RenderEmailCode(platform.EmailCodeData{
		Brand:        s.cfg.BrandName,
		Code:         code,
		Purpose:      v.Purpose,
		Email:        v.Email,
		TTLMinutes:   ttlMinutes,
		RequestIP:    v.RequestIP,
		SupportEmail: s.cfg.SupportEmail,
	})
	if err != nil {
		s.fail(ctx, v, "render email: "+err.Error())
		return fmt.Errorf("render email: %w", err)
	}
	msg := platform.MailMessage{To: v.Email, Subject: subject, HTML: html, Text: text}

	var lastErr error
	backoff := 500 * time.Millisecond
	for attempt := 1; attempt <= 3; attempt++ {
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		lastErr = s.mailer.Send(sendCtx, msg)
		cancel()
		if lastErr == nil {
			break
		}
		s.log.Warn("email send attempt failed",
			"verification_id", v.ID, "provider", s.mailer.Provider(), "attempt", attempt, "error", lastErr)
		if ctx.Err() != nil {
			break
		}
		if attempt < 3 {
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	if lastErr != nil {
		s.fail(ctx, v, lastErr.Error())
		return fmt.Errorf("send email: %w", lastErr)
	}

	if err := s.repo.MarkDelivered(ctx, v.ID); err != nil {
		return fmt.Errorf("mark delivered: %w", err)
	}
	metrics.EmailDeliveries.WithLabelValues(s.mailer.Provider(), "ok").Inc()
	s.enqueue(ctx, domain.EventEmailDelivered, v, events.EmailDelivered{
		VerificationID: v.ID, Email: v.Email, Purpose: v.Purpose, Provider: s.mailer.Provider(),
	})
	s.log.Info("email code delivered", "verification_id", v.ID, "provider", s.mailer.Provider(), "purpose", v.Purpose)
	return nil
}

func (s *EmailDeliveryService) fail(ctx context.Context, v *domain.EmailVerification, reason string) {
	if err := s.repo.MarkDeliveryFailed(ctx, v.ID, reason); err != nil {
		s.log.Warn("mark delivery failed", "verification_id", v.ID, "error", err)
	}
	metrics.EmailDeliveries.WithLabelValues(s.mailer.Provider(), "error").Inc()
	s.enqueue(ctx, domain.EventEmailFailed, v, events.EmailDeliveryFailed{
		VerificationID: v.ID, Email: v.Email, Purpose: v.Purpose,
		Provider: s.mailer.Provider(), Error: reason,
	})
}

func (s *EmailDeliveryService) enqueue(ctx context.Context, eventType string, v *domain.EmailVerification, payload any) {
	ev := events.NewEvent(eventType, v.ID.String(), payload)
	msg, err := outboxFor(ev)
	if err != nil {
		s.log.Warn("encode delivery event", "event_type", eventType, "error", err)
		return
	}
	enqueueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.repo.Enqueue(enqueueCtx, msg); err != nil {
		s.log.Warn("enqueue delivery event", "event_type", eventType, "error", err)
	}
}
