package http_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/platform"
	httptransport "github.com/getcontact/backend/internal/transport/http"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := &config.Config{
		Env: "test", ServiceName: "getcontact-api", Version: "test",
		HTTP: config.HTTPConfig{RequestTimeout: 5 * time.Second},
		Rate: config.RateConfig{Enabled: false},
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:3000"},
			AllowedMethods: []string{"GET", "POST", "OPTIONS"},
			AllowedHeaders: []string{"Authorization", "Content-Type"},
		},
		Kafka: config.KafkaConfig{Enabled: false},
	}
	tm, err := platform.NewTokenManager("0123456789abcdef0123456789abcdef", "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return httptransport.New(httptransport.Deps{
		Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test", Tokens: tm,
	})
}

func TestOperationalEndpoints(t *testing.T) {
	h := newTestHandler(t)
	tests := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{"/healthz", http.StatusOK, `"status":"ok"`},
		{"/version", http.StatusOK, `"version":"test"`},
		{"/", http.StatusOK, `"name":"GetContact API"`},
		{"/openapi.yaml", http.StatusOK, "openapi:"},
		{"/docs", http.StatusOK, "Swagger"},
		{"/docs/assets/swagger-ui.css", http.StatusOK, ".swagger-ui"},
		{"/does-not-exist", http.StatusNotFound, `"code":"NOT_FOUND"`},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tt.wantStatus {
			t.Errorf("%s: status %d, want %d", tt.path, rec.Code, tt.wantStatus)
		}
		if !strings.Contains(rec.Body.String(), tt.wantBody) {
			t.Errorf("%s: body %q does not contain %q", tt.path, rec.Body.String(), tt.wantBody)
		}
	}
}

func TestSecurityHeadersAndCORS(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff header")
	}
}

func TestAuthAndValidation(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without token: %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"phone":`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed login: %d (%s)", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed: %d", rec.Code)
	}
}
