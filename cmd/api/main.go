// Command api runs the public GetContact HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/getcontact/backend/internal/platform"
	"github.com/getcontact/backend/internal/repository/postgres"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/getcontact/backend/internal/service"
	httptransport "github.com/getcontact/backend/internal/transport/http"
	"github.com/getcontact/backend/migrations"
)

// Version is injected at build time:
//
//	go build -ldflags "-X main.Version=1.2.3" ./cmd/api
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
	log := platform.NewLogger(cfg.Env, cfg.ServiceName, Version)
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

	tm, err := platform.NewTokenManager(cfg.Auth.JWTSecret, cfg.Auth.Issuer, cfg.Auth.AccessTTL)
	if err != nil {
		return err
	}

	userRepo := postgres.NewUserRepo(pool)
	contactRepo := postgres.NewContactRepo(pool)
	numberRepo := postgres.NewNumberRepo(pool)
	tokenRepo := postgres.NewTokenRepo(pool)
	auditRepo := postgres.NewAuditRepo(pool)
	emailRepo := postgres.NewEmailRepo(pool)

	argon := platform.ArgonParams{
		Memory: cfg.Auth.ArgonMemory, Iterations: cfg.Auth.ArgonIterations,
		Threads: cfg.Auth.ArgonThreads, KeyLen: cfg.Auth.ArgonKeyLen,
	}
	authSvc := service.NewAuthService(userRepo, tokenRepo, cache, tm, cfg.Auth.RefreshTTL, argon, publisher, log)
	emailSvc := service.NewEmailAuthService(cfg.Email, cfg.Env, userRepo, emailRepo, authSvc, log)
	authSvc.SetEmailAuth(emailSvc)
	userSvc := service.NewUserService(userRepo, contactRepo, numberRepo, tokenRepo, argon, log)
	contactSvc := service.NewContactService(contactRepo, publisher, log)
	numberSvc := service.NewNumberService(numberRepo, cache, publisher, log, cfg.Redis.CacheTTL)
	searchSvc := service.NewSearchService(numberRepo, contactRepo)
	adminSvc := service.NewAdminService(userRepo, numberRepo, contactRepo, tokenRepo, auditRepo, cache, publisher, log)

	if err := authSvc.BootstrapAdmin(ctx, cfg.Admin.Phone, cfg.Admin.Password, cfg.Admin.Name); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	go adminSvc.ScheduleTokenCleanup(ctx, tokenRepo, 6*time.Hour)
	// The relay turns committed outbox rows into Kafka events; a crash between
	// the database commit and the publish can therefore never lose an email.
	relay := service.NewOutboxRelay(emailRepo, publisher, log,
		cfg.Email.OutboxInterval, cfg.Email.OutboxBatch, cfg.Email.OutboxMaxTries)
	go relay.Run(ctx)

	handler := httptransport.New(httptransport.Deps{
		Config: cfg, Log: log, Version: Version,
		Auth: authSvc, Email: emailSvc, Users: userSvc,
		Contacts: contactSvc, Numbers: numberSvc,
		Search: searchSvc, Admin: adminSvc, Tokens: tm, Cache: cache, DB: pool, Publisher: publisher,
	})

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.HTTP.Addr, "env", cfg.Env, "version", Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("api stopped cleanly")
	return nil
}
