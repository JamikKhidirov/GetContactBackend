package httptransport

import (
	"context"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// authenticate validates the Bearer JWT and stores claims in the request context.
func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			writeError(w, r, a.log, apperr.Unauthorized("missing Bearer token"))
			return
		}
		claims, err := a.tokens.Parse(raw)
		if err != nil {
			writeError(w, r, a.log, apperr.Unauthorized("invalid or expired access token"))
			return
		}
		if a.cache.IsAccessTokenDenied(r.Context(), claims.ID) {
			writeError(w, r, a.log, apperr.Unauthorized("access token has been revoked"))
			return
		}
		if a.cache.IsSessionDenied(r.Context(), claims.SessionID) {
			writeError(w, r, a.log, apperr.Unauthorized("session has been revoked"))
			return
		}
		ctx := withClaims(r.Context(), claims.UserID(), roleFromClaims(claims.Role), claims.ID, claims.SessionID, claims.ExpiresAt.Time)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// optionalAuth attaches claims when a valid token is present but never rejects
// anonymous callers. Used by the public caller-ID lookup endpoint.
func (a *API) optionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			next.ServeHTTP(w, r)
			return
		}
		claims, err := a.tokens.Parse(raw)
		if err != nil || a.cache.IsAccessTokenDenied(r.Context(), claims.ID) || a.cache.IsSessionDenied(r.Context(), claims.SessionID) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := withClaims(r.Context(), claims.UserID(), roleFromClaims(claims.Role), claims.ID, claims.SessionID, claims.ExpiresAt.Time)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *API) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if roleFrom(r.Context()) != "admin" {
			writeError(w, r, a.log, apperr.Forbidden("admin role required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func roleFromClaims(role string) domain.Role {
	if role == string(domain.RoleAdmin) {
		return domain.RoleAdmin
	}
	return domain.RoleUser
}

// rateLimit applies a fixed-window per-IP limit backed by Redis. It fails open
// when Redis is unavailable so a cache outage cannot take down the API.
func (a *API) rateLimit(scope string, limit int) func(http.Handler) http.Handler {
	if !a.cfg.Rate.Enabled || limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	window := a.cfg.Rate.Window
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			count, err := a.cache.Incr(r.Context(), "gc:rl:"+scope+":"+ip, window)
			if err != nil {
				a.log.Warn("rate limiter unavailable, failing open", "error", err)
				next.ServeHTTP(w, r)
				return
			}
			remaining := int64(limit) - count
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
			w.Header().Set("X-RateLimit-Window", window.String())
			if count > int64(limit) {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				writeError(w, r, a.log, apperr.TooManyRequests())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// observability records Prometheus metrics and structured access logs.
func (a *API) observability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		metrics.HTTPInFlight.Inc()
		defer metrics.HTTPInFlight.Dec()

		next.ServeHTTP(ww, r)

		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		metrics.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
		metrics.HTTPDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
		metrics.HTTPResponseSize.WithLabelValues(r.Method, route).Observe(float64(ww.BytesWritten()))

		a.log.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", route,
			"status", status,
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r),
			"request_id", middleware.GetReqID(r.Context()),
			"user_id", userIDString(r.Context()),
		)
	})
}

// recoverer converts panics into JSON 500 responses and logs the stack.
func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.log.Error("panic recovered",
					"panic", rec,
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", middleware.GetReqID(r.Context()),
					"stack", string(debug.Stack()),
				)
				if rw, ok := w.(interface{ Status() int }); ok && rw.Status() != 0 {
					return // response already partially written
				}
				var payload errorPayload
				payload.Error.Code = "INTERNAL_ERROR"
				payload.Error.Message = "internal server error"
				payload.Error.RequestID = middleware.GetReqID(r.Context())
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_ = writeRawJSON(w, payload)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cors implements a strict allow-list CORS policy.
func (a *API) cors(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(a.cfg.CORS.AllowedOrigins))
	wildcard := false
	for _, o := range a.cfg.CORS.AllowedOrigins {
		if o == "*" {
			wildcard = true
		}
		allowed[o] = struct{}{}
	}
	methods := strings.Join(a.cfg.CORS.AllowedMethods, ", ")
	headers := strings.Join(a.cfg.CORS.AllowedHeaders, ", ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			_, ok := allowed[origin]
			switch {
			case wildcard:
				w.Header().Set("Access-Control-Allow-Origin", "*")
			case ok:
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			if wildcard || ok {
				w.Header().Set("Access-Control-Allow-Methods", methods)
				w.Header().Set("Access-Control-Allow-Headers", headers)
				w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id, X-RateLimit-Limit, X-RateLimit-Remaining")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adds defensive headers to every response.
func (a *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		if a.cfg.IsProduction() {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// requestTimeout cancels handlers that exceed the configured budget.
func (a *API) requestTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), a.cfg.HTTP.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP returns the caller IP, honouring the standard proxy headers used by
// the Docker/Kubernetes ingress so rate limiting and session metadata see the
// real mobile client instead of the proxy address.
func clientIP(r *http.Request) string {
	if ip := firstForwardedIP(r.Header.Get("X-Forwarded-For")); ip != "" {
		return ip
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(ip) != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// firstForwardedIP parses the left-most valid address of an X-Forwarded-For
// chain. Invalid or spoofed non-IP values are ignored.
func firstForwardedIP(header string) string {
	for _, part := range strings.Split(header, ",") {
		ip := strings.TrimSpace(part)
		if ip == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}

func userIDString(ctx context.Context) string {
	if id, ok := userIDFrom(ctx); ok {
		return id.String()
	}
	return ""
}
