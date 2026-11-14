// Package integration_test contains black-box HTTP tests against a running
// GetContact stack. They are skipped unless API_URL is set:
//
//	API_URL=http://localhost:8080 go test ./test/integration -v
package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type apiClient struct {
	base  string
	token string
}

func newClient(t *testing.T) *apiClient {
	t.Helper()
	base := os.Getenv("API_URL")
	if base == "" {
		t.Skip("API_URL is not set; skipping integration test")
	}
	return &apiClient{base: base}
}

func (c *apiClient) request(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	return c.requestWithHeaders(t, method, path, token, body, nil)
}

func (c *apiClient) requestWithHeaders(t *testing.T, method, path, token string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp.StatusCode, decoded
}

func expect(t *testing.T, step string, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		raw, _ := json.Marshal(body)
		t.Fatalf("%s: status %d, want %d, body: %s", step, got, want, raw)
	}
}

func data(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	d, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no object data: %v", body)
	}
	return d
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok || v == "" {
		t.Fatalf("field %q missing in %v", key, m)
	}
	return v
}

func waitReady(t *testing.T, c *apiClient) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(c.base + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("API did not become ready within 90s")
}

func TestEndToEndUserJourney(t *testing.T) {
	c := newClient(t)
	waitReady(t, c)

	suffix := time.Now().UnixNano() % 10_000_000
	phone := fmt.Sprintf("+7999%07d", suffix)
	contactPhone := fmt.Sprintf("+7998%07d", suffix)
	password := "Passw0rd123"

	// Registration.
	status, body := c.request(t, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"phone": phone, "password": password, "display_name": "E2E Tester", "email": fmt.Sprintf("e2e%d@example.com", suffix),
	})
	expect(t, "register", status, http.StatusCreated, body)
	user := data(t, body)["user"].(map[string]any)
	userID := str(t, user, "id")
	tokens := data(t, body)["tokens"].(map[string]any)
	access := str(t, tokens, "access_token")
	refresh := str(t, tokens, "refresh_token")
	c.token = access
	t.Logf("registered user %s / %s", userID, phone)

	// Duplicate registration must conflict.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"phone": phone, "password": password, "display_name": "Duplicate",
	})
	expect(t, "duplicate register", status, http.StatusConflict, body)

	// Login + profile.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": password,
	})
	expect(t, "login", status, http.StatusOK, body)
	access = str(t, data(t, body)["tokens"].(map[string]any), "access_token")

	status, body = c.request(t, http.MethodGet, "/api/v1/auth/me", access, nil)
	expect(t, "me", status, http.StatusOK, body)
	if got := str(t, data(t, body), "phone"); got != phone {
		t.Fatalf("me phone %s, want %s", got, phone)
	}

	status, body = c.request(t, http.MethodPatch, "/api/v1/users/me", access, map[string]any{
		"display_name": "E2E Tester Updated",
	})
	expect(t, "update profile", status, http.StatusOK, body)

	// Contacts CRUD.
	status, body = c.request(t, http.MethodPost, "/api/v1/contacts", access, map[string]any{
		"phone": contactPhone, "name": "Alice E2E", "email": "alice@example.com", "is_favorite": true,
	})
	expect(t, "create contact", status, http.StatusCreated, body)
	contactID := str(t, data(t, body), "id")

	status, body = c.request(t, http.MethodGet, "/api/v1/contacts?page=1&per_page=10&q=Alice", access, nil)
	expect(t, "list contacts", status, http.StatusOK, body)
	meta := body["meta"].(map[string]any)
	if int(meta["total"].(float64)) < 1 {
		t.Fatal("created contact not returned by list")
	}

	status, body = c.request(t, http.MethodPost, "/api/v1/contacts/import", access, map[string]any{
		"contacts": []map[string]any{
			{"phone": fmt.Sprintf("+7997%07d", suffix), "name": "Bob Import"},
			{"phone": fmt.Sprintf("+7996%07d", suffix), "name": "Carol Import", "is_favorite": false},
		},
	})
	expect(t, "import contacts", status, http.StatusOK, body)
	if int(data(t, body)["imported"].(float64)) != 2 {
		t.Fatalf("import result: %v", body)
	}

	status, body = c.request(t, http.MethodPatch, "/api/v1/contacts/"+contactID, access, map[string]any{
		"note": "met at conference",
	})
	expect(t, "update contact", status, http.StatusOK, body)

	// Caller-ID lookup + community tags.
	lookupPhone := fmt.Sprintf("+7995%07d", suffix)
	status, body = c.request(t, http.MethodGet, "/api/v1/numbers/"+lookupPhone, access, nil)
	expect(t, "lookup number", status, http.StatusOK, body)
	if got := str(t, data(t, body)["number"].(map[string]any), "phone"); got != lookupPhone {
		t.Fatalf("lookup phone %s, want %s", got, lookupPhone)
	}

	status, body = c.request(t, http.MethodPost, "/api/v1/numbers/"+lookupPhone+"/tags", access, map[string]any{
		"tag": "Telemarketing",
	})
	expect(t, "add tag", status, http.StatusCreated, body)
	tagID := str(t, data(t, body), "id")

	status, body = c.request(t, http.MethodGet, "/api/v1/numbers/"+lookupPhone+"/tags", access, nil)
	expect(t, "list tags", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodDelete, "/api/v1/numbers/"+lookupPhone+"/tags/"+tagID, access, nil)
	expect(t, "delete tag", status, http.StatusNoContent, body)

	status, body = c.request(t, http.MethodPost, "/api/v1/numbers/"+lookupPhone+"/spam", access, map[string]any{
		"category": "telemarketing", "comment": "calls every day",
	})
	expect(t, "report spam", status, http.StatusCreated, body)
	if score := data(t, body)["number"].(map[string]any)["spam_score"].(float64); score <= 0 {
		t.Fatalf("expected positive spam score, got %v", score)
	}

	status, body = c.request(t, http.MethodPost, "/api/v1/numbers/"+lookupPhone+"/spam", access, map[string]any{
		"category": "spam",
	})
	expect(t, "duplicate spam report", status, http.StatusConflict, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/numbers/spam/top?limit=5", access, nil)
	expect(t, "top spam", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/numbers/trending?limit=5", access, nil)
	expect(t, "trending numbers", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/spam/categories", access, nil)
	expect(t, "spam categories", status, http.StatusOK, body)
	if len(body["data"].([]any)) == 0 {
		t.Fatal("spam category catalog is empty")
	}

	status, body = c.request(t, http.MethodGet, "/api/v1/numbers/"+lookupPhone+"/spam", access, nil)
	expect(t, "spam summary", status, http.StatusOK, body)
	if total := data(t, body)["total"].(float64); total < 1 {
		t.Fatalf("spam summary total %v, want >= 1", total)
	}

	status, body = c.request(t, http.MethodPost, "/api/v1/numbers/lookup", access, map[string]any{
		"phones": []string{lookupPhone, "+7 (999) 000-00-00", "not-a-phone"},
	})
	expect(t, "batch lookup", status, http.StatusOK, body)
	batch := data(t, body)
	if int(batch["requested"].(float64)) != 3 || int(batch["returned"].(float64)) != 2 {
		t.Fatalf("batch lookup counts: %v", batch)
	}

	status, body = c.request(t, http.MethodPost, "/api/v1/numbers/"+lookupPhone+"/names", access, map[string]any{
		"name": "E2E Spam Caller",
	})
	expect(t, "add number name", status, http.StatusCreated, body)
	nameID := str(t, data(t, body), "id")

	status, body = c.request(t, http.MethodGet, "/api/v1/numbers/"+lookupPhone+"/names", access, nil)
	expect(t, "number names", status, http.StatusOK, body)
	if len(body["data"].([]any)) < 1 {
		t.Fatal("number name was not aggregated")
	}

	status, body = c.request(t, http.MethodDelete, "/api/v1/numbers/"+lookupPhone+"/names/"+nameID, access, nil)
	expect(t, "delete number name", status, http.StatusNoContent, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/users/me/stats", access, nil)
	expect(t, "user stats", status, http.StatusOK, body)
	if int(data(t, body)["contacts"].(float64)) < 3 {
		t.Fatalf("user stats contacts: %v", body)
	}

	status, body = c.request(t, http.MethodGet, "/api/v1/tags?limit=10", access, nil)
	expect(t, "tag catalog", status, http.StatusOK, body)

	// Unified search.
	status, body = c.request(t, http.MethodGet, "/api/v1/search?q=Alice&limit=10", access, nil)
	expect(t, "search", status, http.StatusOK, body)
	if len(data(t, body)["contacts"].([]any)) < 1 {
		t.Fatal("search did not find the contact")
	}

	status, body = c.request(t, http.MethodDelete, "/api/v1/contacts/"+contactID, access, nil)
	expect(t, "delete contact", status, http.StatusNoContent, body)

	// Refresh rotation: the old refresh token becomes single-use.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": refresh})
	expect(t, "refresh", status, http.StatusOK, body)
	rotated := data(t, body)["tokens"].(map[string]any)
	access = str(t, rotated, "access_token")
	newRefresh := str(t, rotated, "refresh_token")

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": refresh})
	expect(t, "refresh reuse", status, http.StatusUnauthorized, body)

	// Logout blacklists the access token.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/logout", access, map[string]any{"refresh_token": newRefresh})
	expect(t, "logout", status, http.StatusNoContent, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/auth/me", access, nil)
	expect(t, "me after logout", status, http.StatusUnauthorized, body)

	// Admin journey (uses bootstrap credentials from the environment).
	adminPhone := env("ADMIN_PHONE", "+79990000000")
	adminPassword := env("ADMIN_PASSWORD", "Admin12345")
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": adminPhone, "password": adminPassword,
	})
	if status != http.StatusOK {
		t.Logf("admin login unavailable (status %d); skipping admin checks", status)
		return
	}
	adminAccess := str(t, data(t, body)["tokens"].(map[string]any), "access_token")

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/stats", adminAccess, nil)
	expect(t, "admin stats", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/users?page=1&per_page=10", adminAccess, nil)
	expect(t, "admin users", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodPatch, "/api/v1/admin/users/"+userID+"/block", adminAccess, map[string]any{"blocked": true})
	expect(t, "admin block", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": password,
	})
	expect(t, "blocked login", status, http.StatusForbidden, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/users/"+userID, adminAccess, nil)
	expect(t, "admin get user", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/reports?page=1&per_page=10", adminAccess, nil)
	expect(t, "admin reports", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/users/"+userID+"/summary", adminAccess, nil)
	expect(t, "admin user summary", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/users/"+userID+"/sessions", adminAccess, nil)
	expect(t, "admin user sessions", status, http.StatusOK, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/numbers/"+lookupPhone, adminAccess, nil)
	expect(t, "admin number dossier", status, http.StatusOK, body)
	dossier := data(t, body)
	if got := str(t, dossier["number"].(map[string]any), "phone"); got != lookupPhone {
		t.Fatalf("dossier phone %s, want %s", got, lookupPhone)
	}

	status, body = c.request(t, http.MethodPatch, "/api/v1/admin/numbers/"+lookupPhone, adminAccess, map[string]any{
		"name": "E2E Admin Name", "carrier": "MTS",
	})
	expect(t, "admin update number", status, http.StatusOK, body)
	if got := str(t, data(t, body), "name"); got != "E2E Admin Name" {
		t.Fatalf("admin updated name %s", got)
	}

	status, body = c.request(t, http.MethodGet, "/api/v1/admin/audit?page=1&per_page=10", adminAccess, nil)
	expect(t, "admin audit", status, http.StatusOK, body)

	// Moderation: find the report created above and delete it. The number's
	// spam score and caller-ID cache must be recomputed by the service.
	status, body = c.request(t, http.MethodGet, "/api/v1/admin/reports?page=1&per_page=100", adminAccess, nil)
	expect(t, "admin reports for delete", status, http.StatusOK, body)
	reportID := ""
	for _, item := range body["data"].([]any) {
		report, _ := item.(map[string]any)
		if report["phone"] == lookupPhone {
			reportID = str(t, report, "id")
			break
		}
	}
	if reportID == "" {
		t.Fatalf("report for %s not found in admin queue", lookupPhone)
	}
	status, body = c.request(t, http.MethodDelete, "/api/v1/admin/reports/"+reportID, adminAccess, nil)
	expect(t, "admin delete report", status, http.StatusNoContent, body)

	status, body = c.request(t, http.MethodPatch, "/api/v1/admin/users/"+userID+"/block", adminAccess, map[string]any{"blocked": false})
	expect(t, "admin unblock", status, http.StatusOK, body)

	// Password change: old credentials stop working, new ones start.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": password,
	})
	expect(t, "login after unblock", status, http.StatusOK, body)
	access = str(t, data(t, body)["tokens"].(map[string]any), "access_token")

	// An admin can end every session, which must invalidate the live access
	// token immediately.
	status, body = c.request(t, http.MethodDelete, "/api/v1/admin/users/"+userID+"/sessions", adminAccess, nil)
	expect(t, "admin revoke sessions", status, http.StatusOK, body)
	status, body = c.request(t, http.MethodGet, "/api/v1/auth/me", access, nil)
	expect(t, "me after admin session revoke", status, http.StatusUnauthorized, body)

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": password,
	})
	expect(t, "login after admin session revoke", status, http.StatusOK, body)
	access = str(t, data(t, body)["tokens"].(map[string]any), "access_token")

	newPassword := "NewPassw0rd456"
	status, body = c.request(t, http.MethodPut, "/api/v1/users/me/password", access, map[string]any{
		"current_password": password, "new_password": newPassword,
	})
	expect(t, "change password", status, http.StatusNoContent, body)

	// token_version was bumped, so the access token issued before the change
	// is rejected even though its TTL has not expired.
	status, body = c.request(t, http.MethodGet, "/api/v1/auth/me", access, nil)
	expect(t, "me after password change", status, http.StatusUnauthorized, body)

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": password,
	})
	expect(t, "login with old password", status, http.StatusUnauthorized, body)

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": newPassword,
	})
	expect(t, "login with new password", status, http.StatusOK, body)
	access = str(t, data(t, body)["tokens"].(map[string]any), "access_token")

	// Own profile is not masked; account deletion is cascade and final.
	status, body = c.request(t, http.MethodGet, "/api/v1/users/"+userID, access, nil)
	expect(t, "own public profile", status, http.StatusOK, body)
	if got := str(t, data(t, body), "phone"); got != phone {
		t.Fatalf("own profile phone %s, want %s", got, phone)
	}

	status, body = c.request(t, http.MethodDelete, "/api/v1/users/me", access, nil)
	expect(t, "delete account", status, http.StatusNoContent, body)

	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": newPassword,
	})
	expect(t, "login after deletion", status, http.StatusUnauthorized, body)
}

func TestDeviceSessionsAndMetadata(t *testing.T) {
	c := newClient(t)
	waitReady(t, c)

	suffix := time.Now().UnixNano() % 10_000_000
	phone := fmt.Sprintf("+7994%07d", suffix)
	password := "Passw0rd123"

	deviceHeaders := map[string]string{
		"X-Device-Id":           "device-alpha",
		"X-Device-Name":         strings.Repeat("Устройство", 20),
		"X-Device-Model":        "Pixel 9 Pro",
		"X-Device-Manufacturer": "Google",
		"X-OS":                  "Android",
		"X-OS-Version":          "15",
		"X-App-Version":         "1.2.3",
		"X-App-Build":           "12345",
		"X-Client":              "android",
		"X-Locale":              "ru-RU",
		"X-Timezone":            "Europe/Moscow",
		"X-Network-Type":        "wifi",
		"X-Forwarded-For":       "203.0.113.7",
	}

	status, body := c.requestWithHeaders(t, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"phone": phone, "password": password, "display_name": "Session Tester",
	}, deviceHeaders)
	expect(t, "register with device", status, http.StatusCreated, body)
	first := data(t, body)["tokens"].(map[string]any)
	access1 := str(t, first, "access_token")

	// The owning user sees the captured metadata and the current-session marker.
	status, body = c.request(t, http.MethodGet, "/api/v1/auth/sessions", access1, nil)
	expect(t, "list sessions", status, http.StatusOK, body)
	sessions := data(t, body)["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d: %v", len(sessions), sessions)
	}
	session := sessions[0].(map[string]any)
	if session["current"] != true {
		t.Fatalf("expected current session, got %v", session)
	}
	if got := str(t, session, "device_id"); got != "device-alpha" {
		t.Fatalf("device_id %q", got)
	}
	if got := str(t, session, "os"); got != "Android" {
		t.Fatalf("os %q", got)
	}
	if got := str(t, session, "app_version"); got != "1.2.3" {
		t.Fatalf("app_version %q", got)
	}
	// X-Forwarded-For is only honoured when the direct peer is listed in
	// TRUSTED_PROXIES. The dev stack does not trust the host gateway, so the
	// spoofed address must be ignored and the real socket IP stored instead.
	if got := str(t, session, "ip"); got == "203.0.113.7" {
		t.Fatalf("spoofed X-Forwarded-For was trusted: %q", got)
	} else if got == "" {
		t.Fatal("session ip is empty")
	}
	// Long non-ASCII metadata is truncated by runes, not bytes, so it stays
	// valid UTF-8 and fits the VARCHAR(128) column.
	if name := str(t, session, "device_name"); utf8.RuneCountInString(name) != 128 {
		t.Fatalf("device_name rune length %d, want 128", utf8.RuneCountInString(name))
	}

	// A second login (another device) adds a session. This one also exercises
	// the JSON device block instead of headers.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": password,
		"device": map[string]any{
			"id": "device-beta", "name": "iPhone 16", "model": "iPhone17,1",
			"manufacturer": "Apple", "os": "iOS", "os_version": "18.2",
			"app_version": "1.2.3", "client": "ios",
		},
	})
	expect(t, "second login", status, http.StatusOK, body)
	second := data(t, body)["tokens"].(map[string]any)
	access2 := str(t, second, "access_token")
	refresh2 := str(t, second, "refresh_token")

	status, body = c.request(t, http.MethodGet, "/api/v1/auth/sessions", access1, nil)
	expect(t, "list sessions after login", status, http.StatusOK, body)
	sessions = data(t, body)["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	var betaID string
	for _, item := range sessions {
		m := item.(map[string]any)
		if m["device_id"] == "device-beta" {
			betaID = str(t, m, "id")
			if m["current"] != false {
				t.Fatalf("beta session must not be current: %v", m)
			}
		}
	}
	if betaID == "" {
		t.Fatal("device-beta session not listed")
	}

	// Revoking the second session immediately invalidates its access token.
	status, body = c.request(t, http.MethodDelete, "/api/v1/auth/sessions/"+betaID, access1, nil)
	expect(t, "revoke session", status, http.StatusNoContent, body)

	status, body = c.request(t, http.MethodGet, "/api/v1/auth/me", access2, nil)
	expect(t, "revoked session access token", status, http.StatusUnauthorized, body)

	// The revoked refresh token can no longer be rotated.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refresh_token": refresh2})
	expect(t, "revoked session refresh", status, http.StatusUnauthorized, body)

	// The original session still works.
	status, body = c.request(t, http.MethodGet, "/api/v1/auth/me", access1, nil)
	expect(t, "surviving session", status, http.StatusOK, body)

	// Unknown session ids are rejected without leaking existence.
	status, body = c.request(t, http.MethodDelete, "/api/v1/auth/sessions/00000000-0000-4000-8000-000000000000", access1, nil)
	expect(t, "revoke unknown session", status, http.StatusNotFound, body)

	// Logout everywhere ends the surviving session and revokes its access token.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/logout/all", access1, nil)
	expect(t, "logout all", status, http.StatusNoContent, body)
	status, body = c.request(t, http.MethodGet, "/api/v1/auth/me", access1, nil)
	expect(t, "me after logout all", status, http.StatusUnauthorized, body)
}

func TestMetricsAndDocs(t *testing.T) {
	c := newClient(t)
	waitReady(t, c)

	resp, err := http.Get(c.base + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte("getcontact_http_requests_total")) {
		t.Fatalf("unexpected metrics response (%d)", resp.StatusCode)
	}

	resp, err = http.Get(c.base + "/openapi.yaml")
	if err != nil {
		t.Fatalf("openapi: %v", err)
	}
	defer resp.Body.Close()
	raw, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte("openapi: 3.0.3")) {
		t.Fatalf("unexpected openapi response (%d)", resp.StatusCode)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
