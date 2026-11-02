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
	"testing"
	"time"
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

	status, body = c.request(t, http.MethodPatch, "/api/v1/admin/users/"+userID+"/block", adminAccess, map[string]any{"blocked": false})
	expect(t, "admin unblock", status, http.StatusOK, body)

	// Password change: old credentials stop working, new ones start.
	status, body = c.request(t, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"phone": phone, "password": password,
	})
	expect(t, "login after unblock", status, http.StatusOK, body)
	access = str(t, data(t, body)["tokens"].(map[string]any), "access_token")

	newPassword := "NewPassw0rd456"
	status, body = c.request(t, http.MethodPut, "/api/v1/users/me/password", access, map[string]any{
		"current_password": password, "new_password": newPassword,
	})
	expect(t, "change password", status, http.StatusNoContent, body)

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
