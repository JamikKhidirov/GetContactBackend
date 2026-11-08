package integration_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// testEmail returns a unique Gmail plus-address so every run lands in the same
// inbox without colliding with a previous registration. Override the mailbox
// with EMAIL_TEST_ADDRESS when needed (defaults to the project test inbox).
func testEmail(suffix int64) string {
	base := strings.TrimSpace(os.Getenv("EMAIL_TEST_ADDRESS"))
	if base == "" {
		base = "dzamikhidirov8@gmail.com"
	}
	at := strings.LastIndexByte(base, '@')
	if at <= 0 {
		return fmt.Sprintf("getcontact+%d@example.com", suffix)
	}
	return fmt.Sprintf("%s+gc%d%s", base[:at], suffix, base[at:])
}

// TestEmailCodeAuthJourney exercises the full passwordless flow: signup code,
// verified registration, email login, replay protection and address
// verification. In development the log provider echoes the code as dev_code;
// with SMTP configured the same request is delivered to the real inbox.
func TestEmailCodeAuthJourney(t *testing.T) {
	c := newClient(t)
	waitReady(t, c)

	suffix := time.Now().UnixNano() % 1_000_000_000
	email := testEmail(suffix)
	phone := fmt.Sprintf("+7977%07d", time.Now().UnixNano()%10_000_000)
	password := "Passw0rd123"

	// 1. Request a signup code. If the stack uses a real SMTP provider the code
	// is not echoed; the request itself has still exercised the outbox.
	status, body := c.request(t, http.MethodPost, "/api/v1/auth/email/code", "", map[string]any{
		"email": email, "purpose": "signup",
	})
	expect(t, "request signup code", status, http.StatusAccepted, body)
	signup := data(t, body)
	devCode := strOrEmpty(signup, "dev_code")
	if devCode == "" {
		t.Logf("email provider does not echo dev_code; signup email was queued for %s", email)
		t.Skip("set EMAIL_PROVIDER=log and EMAIL_DEV_ECHO=true to run the full automated journey")
	}
	if got := str(t, signup, "email"); got != email {
		t.Fatalf("normalized email = %q, want %q", got, email)
	}

	// 2. Register with the code; the address is verified at creation.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"phone": phone, "password": password, "display_name": "Email Tester",
		"email": email, "email_code": devCode,
	})
	expect(t, "register with email code", status, http.StatusCreated, body)
	user := data(t, body)["user"].(map[string]any)
	if verified, _ := user["email_verified"].(bool); !verified {
		t.Fatalf("email_verified = false after signup code: %v", user)
	}
	access := str(t, data(t, body)["tokens"].(map[string]any), "access_token")

	// 3. Request a login code and exchange it for a token pair.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/email/code", "", map[string]any{
		"email": email, "purpose": "login",
	})
	expect(t, "request login code", status, http.StatusAccepted, body)
	loginCode := str(t, data(t, body), "dev_code")

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{
		"email": email, "code": loginCode,
		"device": map[string]any{"id": "email-test-device", "name": "Email Test Phone", "os": "Android"},
	})
	expect(t, "email login", status, http.StatusOK, body)
	login := data(t, body)
	if str(t, login["tokens"].(map[string]any), "access_token") == "" {
		t.Fatal("email login returned an empty access token")
	}

	// 4. The same code cannot be replayed.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{
		"email": email, "code": loginCode,
	})
	expect(t, "login code replay", status, http.StatusUnauthorized, body)

	// 5. Verify ownership from an authenticated session: wrong code first,
	// then the real one.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/email/verify", access, map[string]any{
		"email": email,
	})
	expect(t, "request verify code", status, http.StatusAccepted, body)
	verifyCode := str(t, data(t, body), "dev_code")
	wrongCode := "000000"
	if verifyCode == wrongCode {
		wrongCode = "111111"
	}

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/email/verify/confirm", access, map[string]any{
		"email": email, "code": wrongCode,
	})
	expect(t, "confirm wrong verify code", status, http.StatusUnauthorized, body)

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/email/verify/confirm", access, map[string]any{
		"email": email, "code": verifyCode,
	})
	expect(t, "confirm verify code", status, http.StatusOK, body)
	if verified, _ := data(t, body)["email_verified"].(bool); !verified {
		t.Fatalf("email_verified = false after confirmation: %v", body)
	}

	// 6. Invalid addresses are rejected before any event is queued.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/email/code", "", map[string]any{
		"email": "not-an-email", "purpose": "login",
	})
	expect(t, "invalid email", status, http.StatusUnprocessableEntity, body)
}

func strOrEmpty(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
