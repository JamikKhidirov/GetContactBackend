package httptransport

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/getcontact/backend/api"
	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/events"
	"github.com/getcontact/backend/internal/platform"
	rediscache "github.com/getcontact/backend/internal/repository/redis"
	"github.com/getcontact/backend/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Deps is everything the HTTP layer needs, injected from main.
type Deps struct {
	Config    *config.Config
	Log       *slog.Logger
	Version   string
	Auth      *service.AuthService
	Email     *service.EmailAuthService
	Users     *service.UserService
	UserRepo  domain.UserRepository
	Contacts  *service.ContactService
	Numbers   *service.NumberService
	Search    *service.SearchService
	Admin     *service.AdminService
	Tokens    *platform.TokenManager
	Cache     *rediscache.Cache
	DB        *pgxpool.Pool
	Publisher events.Publisher
}

// API owns the router and its dependencies.
type API struct {
	cfg       *config.Config
	log       *slog.Logger
	version   string
	auth      *service.AuthService
	email     *service.EmailAuthService
	users     *service.UserService
	userRepo  domain.UserRepository
	contacts  *service.ContactService
	numbers   *service.NumberService
	searchSvc *service.SearchService
	admin     *service.AdminService
	tokens    *platform.TokenManager
	cache     *rediscache.Cache
	db        *pgxpool.Pool
	pub       events.Publisher
	trusted   []*net.IPNet
}

// New builds the fully wired HTTP handler.
func New(d Deps) http.Handler {
	a := &API{
		cfg: d.Config, log: d.Log, version: d.Version,
		auth: d.Auth, email: d.Email, users: d.Users, userRepo: d.UserRepo,
		contacts: d.Contacts, numbers: d.Numbers,
		searchSvc: d.Search, admin: d.Admin, tokens: d.Tokens, cache: d.Cache,
		db: d.DB, pub: d.Publisher,
		trusted: parseTrustedProxies(d.Config.HTTP.TrustedProxies),
	}

	r := chi.NewRouter()
	// RequestID is outermost; recoverer wraps every other middleware so a panic
	// in CORS, observability or the limiter still yields the JSON 500 envelope.
	r.Use(middleware.RequestID)
	r.Use(a.recoverer)
	r.Use(a.securityHeaders)
	r.Use(a.cors)
	r.Use(a.requestTimeout)
	r.Use(a.observability)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, a.log, apperr.NotFound("endpoint"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, a.log, apperr.New(http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed"))
	})

	// Operational endpoints (never rate limited).
	r.Get("/", a.index)
	r.Get("/healthz", a.healthz)
	r.Get("/readyz", a.readyz)
	r.Get("/version", a.versionHandler)
	r.Handle("/metrics", promhttp.Handler())
	r.Get("/openapi.yaml", a.openAPISpec)
	r.Get("/docs", a.swaggerUI)
	r.Get("/docs/", a.swaggerUI)
	if assets, err := fs.Sub(api.Docs, "swagger"); err == nil {
		r.Handle("/docs/assets/*", http.StripPrefix("/docs/assets/", http.FileServer(http.FS(assets))))
	}

	r.Route("/api/v1", func(r chi.Router) {
		// Public, rate-limited read endpoints.
		r.Group(func(r chi.Router) {
			r.Use(a.rateLimit("public", a.cfg.Rate.PublicRPM))
			r.Get("/tags", a.listTagCatalog)
			r.Get("/spam/categories", a.spamCategories)
			r.Get("/numbers/spam/top", a.topSpam)
			r.Get("/numbers/trending", a.trendingNumbers)
			r.Get("/numbers/{phone}/tags", a.listNumberTags)
			r.Get("/numbers/{phone}/names", a.numberNames)
			r.Get("/numbers/{phone}/spam", a.numberSpamSummary)
			r.With(a.optionalAuth).Get("/numbers/{phone}", a.lookupNumber)
		})

		// Anonymous authentication endpoints get a stricter bucket.
		r.Group(func(r chi.Router) {
			r.Use(a.rateLimit("auth", a.cfg.Rate.AuthRPM))
			r.Post("/auth/register", a.register)
			r.Post("/auth/login", a.login)
			r.Post("/auth/refresh", a.refresh)
			r.With(a.optionalAuth).Post("/auth/email/code", a.requestEmailCode)
			r.Post("/auth/email/login", a.emailLogin)
		})

		// Authenticated endpoints.
		r.Group(func(r chi.Router) {
			r.Use(a.authenticate)
			r.Use(a.rateLimit("user", a.cfg.Rate.PublicRPM))

			r.Post("/auth/logout", a.logout)
			r.Post("/auth/logout/all", a.logoutAll)
			r.Get("/auth/me", a.me)
			r.Get("/auth/sessions", a.listSessions)
			r.Delete("/auth/sessions/{sessionID}", a.revokeSession)
			r.Post("/auth/email/verify", a.requestEmailVerification)
			r.Post("/auth/email/verify/confirm", a.confirmEmailVerification)

			r.Get("/users/me/stats", a.personalStats)
			r.Get("/users/{id}", a.getUser)
			r.Patch("/users/me", a.updateMe)
			r.Put("/users/me/password", a.changePassword)
			r.Delete("/users/me", a.deleteMe)

			r.Get("/contacts", a.listContacts)
			r.Post("/contacts", a.createContact)
			r.Post("/contacts/import", a.importContacts)
			r.Get("/contacts/{id}", a.getContact)
			r.Patch("/contacts/{id}", a.updateContact)
			r.Delete("/contacts/{id}", a.deleteContact)

			r.Post("/numbers/lookup", a.batchLookup)
			r.Post("/numbers/{phone}/tags", a.addTag)
			r.Delete("/numbers/{phone}/tags/{tagID}", a.deleteTag)
			r.Post("/numbers/{phone}/names", a.addNumberName)
			r.Delete("/numbers/{phone}/names/{nameID}", a.deleteNumberName)
			r.Post("/numbers/{phone}/spam", a.reportSpam)

			r.Get("/search", a.search)

			r.Route("/admin", func(r chi.Router) {
				r.Use(a.requireAdmin)
				r.Get("/stats", a.adminStats)
				r.Get("/audit", a.adminAudit)
				r.Get("/users", a.adminListUsers)
				r.Get("/users/{id}", a.adminGetUser)
				r.Get("/users/{id}/summary", a.adminUserSummary)
				r.Get("/users/{id}/sessions", a.adminUserSessions)
				r.Delete("/users/{id}/sessions", a.adminRevokeUserSessions)
				r.Patch("/users/{id}/block", a.adminSetBlocked)
				r.Delete("/users/{id}", a.adminDeleteUser)
				r.Get("/reports", a.adminListReports)
				r.Delete("/reports/{reportID}", a.adminDeleteReport)
				r.Get("/numbers/{phone}", a.adminNumberDossier)
				r.Patch("/numbers/{phone}", a.adminUpdateNumber)
			})
		})
	})
	return r
}

func (a *API) index(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, map[string]any{
		"name":        "GetContact API",
		"description": "Caller-ID, community tags, spam reports and personal contacts",
		"version":     a.version,
		"docs":        "/docs",
		"openapi":     "/openapi.yaml",
		"health":      "/healthz",
		"metrics":     "/metrics",
		"api_base":    "/api/v1",
	})
}

func (a *API) versionHandler(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, map[string]any{
		"version": a.version,
		"service": a.cfg.ServiceName,
		"env":     a.cfg.Env,
		"time":    time.Now().UTC(),
	})
}

type checkResult struct {
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": a.version})
}

func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	checks := map[string]checkResult{}
	healthy := true

	start := time.Now()
	if err := a.db.Ping(ctx); err != nil {
		a.log.Warn("readiness: postgres ping failed", "error", err)
		checks["postgres"] = checkResult{Status: "down", LatencyMS: time.Since(start).Milliseconds(), Error: "unavailable"}
		healthy = false
	} else {
		checks["postgres"] = checkResult{Status: "up", LatencyMS: time.Since(start).Milliseconds()}
	}

	start = time.Now()
	if err := a.cache.Ping(ctx); err != nil {
		a.log.Warn("readiness: redis ping failed", "error", err)
		checks["redis"] = checkResult{Status: "down", LatencyMS: time.Since(start).Milliseconds(), Error: "unavailable"}
		healthy = false
	} else {
		checks["redis"] = checkResult{Status: "up", LatencyMS: time.Since(start).Milliseconds()}
	}

	if a.cfg.Kafka.Enabled {
		start = time.Now()
		if err := a.pub.Ping(ctx); err != nil {
			a.log.Warn("readiness: kafka ping failed", "error", err)
			checks["kafka"] = checkResult{Status: "down", LatencyMS: time.Since(start).Milliseconds(), Error: "unavailable"}
			healthy = false
		} else {
			checks["kafka"] = checkResult{Status: "up", LatencyMS: time.Since(start).Milliseconds()}
		}
	}

	status := http.StatusOK
	overall := "ready"
	if !healthy {
		status = http.StatusServiceUnavailable
		overall = "not_ready"
	}
	writeJSON(w, status, map[string]any{
		"status": overall,
		"checks": checks,
		"time":   time.Now().UTC(),
	})
}

func (a *API) swaggerUI(w http.ResponseWriter, r *http.Request) {
	body, err := api.Docs.ReadFile("swagger.html")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

func (a *API) openAPISpec(w http.ResponseWriter, r *http.Request) {
	body, err := api.Docs.ReadFile("openapi.yaml")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}
