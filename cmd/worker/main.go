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

	consumer, err := events.NewConsumer(cfg.Kafka, publisher, log)
	if err != nil {
		return err
	}
	defer consumer.Close()

	handler := func(ctx context.Context, ev domain.Event) error {
		return handleEvent(ctx, log, cfg.Kafka.Topic, eventRepo, numberRepo, cache, ev)
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
	ev domain.Event,
) error {
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
