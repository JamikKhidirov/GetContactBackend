package config_test

import (
	"testing"

	"github.com/getcontact/backend/internal/config"
)

func setProductionEnv(t *testing.T, jwtSecret, emailProvider, emailSecret string) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_SECRET", jwtSecret)
	t.Setenv("EMAIL_PROVIDER", emailProvider)
	t.Setenv("EMAIL_CODE_SECRET", emailSecret)
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("ADMIN_PHONE", "")
}

func TestProductionRejectsPlaceholderJWTSecret(t *testing.T) {
	setProductionEnv(t, "dev-only-change-me-at-least-32-characters", "smtp",
		"0123456789abcdef0123456789abcdef")
	if _, err := config.Load(); err == nil {
		t.Fatal("placeholder JWT secret accepted in production")
	}
}

func TestProductionRejectsLogEmailProvider(t *testing.T) {
	setProductionEnv(t, "0123456789abcdef0123456789abcdef", "log",
		"0123456789abcdef0123456789abcdef")
	if _, err := config.Load(); err == nil {
		t.Fatal("EMAIL_PROVIDER=log accepted in production")
	}
}

func TestProductionRejectsPlaceholderEmailSecret(t *testing.T) {
	setProductionEnv(t, "0123456789abcdef0123456789abcdef", "smtp",
		"CHANGE_ME_email_code_secret_32_chars_minimum")
	if _, err := config.Load(); err == nil {
		t.Fatal("placeholder EMAIL_CODE_SECRET accepted in production")
	}
}

func TestProductionAcceptsConfiguredSecrets(t *testing.T) {
	setProductionEnv(t, "0123456789abcdef0123456789abcdef", "smtp",
		"fedcba9876543210fedcba9876543210")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
	if cfg.Email.DevEcho {
		t.Fatal("EMAIL_DEV_ECHO must be forced off in production")
	}
}
