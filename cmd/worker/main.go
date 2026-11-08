// Command worker consumes Kafka events, maintains derived data and exposes
// worker metrics on a separate port.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/getcontact/backend/internal/platform"
	"github.com/getcontact/backend/internal/repository/postgres"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/getcontact/backend/internal/service"
	"github.com/getcontact/backend/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Version is injected at build time.
var Version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := platform.NewLogger(cfg.Env, "getcontact-worker", Version)
	metrics.Init(Version)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool, migrations.FS, log); err != nil {
		return err
	}

	cache, err := rediscache.New(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer cache.Close()

	publisher, err := events.NewPublisher(cfg.Kafka)
	if err != nil {
		return err
	}
	defer publisher.Close()

	if !cfg.Kafka.Enabled {
		return errors.New("worker requires KAFKA_ENABLED=true")
	}

	eventRepo := postgres.NewEventRepo(pool)
	numberRepo := postgres.NewNumberRepo(pool)
	emailRepo := postgres.NewEmailRepo(pool)

	mailer, err := platform.NewMailer(platform.MailerOptions{
		Provider: cfg.Email.Provider, FromName: cfg.Email.FromName,
		FromAddress: cfg.Email.FromAddress, ReplyTo: cfg.Email.ReplyTo,
		Host: cfg.Email.SMTPHost, Port: cfg.Email.SMTPPort,
		Username: cfg.Email.SMTPUsername, Password: cfg.Email.SMTPPassword,
		TLS: cfg.Email.SMTPTLS, Timeout: cfg.Email.SMTPTimeout,
	}, log)
	if err != nil {
		return err
	}
	delivery := service.NewEmailDeliveryService(cfg.Email, emailRepo, mailer, log)

	consumer, err := events.NewConsumer(cfg.Kafka, publisher, log)
	if err != nil {
		return err
	}
	defer consumer.Close()

	handler := func(ctx context.Context, ev domain.Event) error {
		return handleEvent(ctx, log, cfg.Kafka.Topic, eventRepo, numberRepo, cache, delivery, ev)
	}

	consumerErr := make(chan error, 1)
	go func() {
		if err := consumer.Run(ctx, handler); err != nil {
			consumerErr <- err
		}
	}()

	metricsSrv := newMetricsServer(cfg.HTTP.MetricsAddr, pool, cache, publisher)
	go func() {
		log.Info("worker metrics listening", "addr", cfg.HTTP.MetricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			consumerErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("worker shutdown signal received")
	case err := <-consumerErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = metricsSrv.Shutdown(shutdownCtx)
	log.Info("worker stopped cleanly")
	return nil
}

// handleEvent persists the event exactly once, then applies side effects.
func handleEvent(
	ctx context.Context,
	log *slog.Logger,
	topic string,
	eventRepo domain.EventRepository,
	numberRepo domain.NumberRepository,
	cache *rediscache.Cache,
	delivery *service.EmailDeliveryService,
	ev domain.Event,
) error {
	// Email delivery runs before the idempotency insert. If the worker crashes
	// after sending but before recording the event, the event is redelivered;
	// the delivered_at marker then prevents a duplicate and the insert retries.
	// The reverse order could swallow an email that was never sent.
	if ev.Type == domain.EventEmailCodeRequested {
		var payload events.EmailCodeRequested
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		if err := delivery.DeliverCode(ctx, payload.VerificationID); err != nil {
			return fmt.Errorf("deliver email code: %w", err)
		}
		_, err := eventRepo.Insert(ctx, topic, ev)
		return err
	}

	inserted, err := eventRepo.Insert(ctx, topic, ev)
	if err != nil {
		return fmt.Errorf("persist event: %w", err)
	}
	if !inserted {
		log.Debug("duplicate event ignored", "event_id", ev.ID)
		return nil
	}

	switch ev.Type {
	case domain.EventSpamReported:
		var payload events.SpamReported
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		if err := numberRepo.RecomputeSpamScore(ctx, payload.Phone); err != nil {
			return fmt.Errorf("recompute spam score: %w", err)
		}
	case domain.EventTagAdded:
		var payload events.TagAdded
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		if err := numberRepo.RecomputeSpamScore(ctx, payload.Phone); err != nil {
			return fmt.Errorf("recompute spam score: %w", err)
		}
	case domain.EventNumberLookedUp:
		if _, err := cache.IncrPersist(ctx, "gc:stats:lookups"); err != nil {
			log.Warn("lookup counter update failed", "error", err)
		}
	case domain.EventUserRegistered:
		var payload events.UserRegistered
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		log.Info("welcome flow triggered", "user_id", payload.UserID)
	case domain.EventEmailDelivered:
		var payload events.EmailDelivered
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		log.Info("email delivery receipt recorded",
			"verification_id", payload.VerificationID, "provider", payload.Provider)
	case domain.EventEmailFailed:
		var payload events.EmailDeliveryFailed
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		log.Warn("email delivery failure recorded",
			"verification_id", payload.VerificationID, "provider", payload.Provider, "reason", payload.Error)
	case domain.EventUserEmailVerified:
		var payload events.UserEmailVerified
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		log.Info("email ownership verified", "user_id", payload.UserID, "email", platform.MaskEmail(payload.Email))
	case domain.EventUserEmailLogin:
		var payload events.UserEmailLogin
		if err := decodePayload(ev.Payload, &payload); err != nil {
			return err
		}
		log.Info("passwordless email login", "user_id", payload.UserID, "session_id", payload.SessionID)
	}
	return nil
}

func decodePayload(payload any, dst any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}

func newMetricsServer(addr string, pool *pgxpool.Pool, cache *rediscache.Cache, publisher events.Publisher) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, `{"status":"not_ready","component":"postgres"}`, http.StatusServiceUnavailable)
			return
		}
		if err := cache.Ping(ctx); err != nil {
			http.Error(w, `{"status":"not_ready","component":"redis"}`, http.StatusServiceUnavailable)
			return
		}
		if err := publisher.Ping(ctx); err != nil {
			http.Error(w, `{"status":"not_ready","component":"kafka"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	return &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}
