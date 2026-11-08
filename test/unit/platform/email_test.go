package platform_test

import (
	"strings"
	"testing"

	"github.com/getcontact/backend/internal/platform"
)

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()
	got, err := platform.NormalizeEmail("  User.Name+Tag@Example.COM ")
	if err != nil {
		t.Fatalf("NormalizeEmail: %v", err)
	}
	if got != "user.name+tag@example.com" {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"", "  ", "not-an-email", "@example.com", "a@b"} {
		if _, err := platform.NormalizeEmail(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestGenerateNumericCode(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := platform.GenerateNumericCode(6)
		if err != nil {
			t.Fatalf("GenerateNumericCode: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("length = %d, want 6: %q", len(code), code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("non-digit in %q", code)
			}
		}
		seen[code] = true
	}
	if len(seen) < 90 {
		t.Fatalf("codes are not varied enough: %d unique", len(seen))
	}
	if _, err := platform.GenerateNumericCode(3); err == nil {
		t.Fatal("expected length validation error")
	}
}

func TestHashAndVerifyEmailCode(t *testing.T) {
	t.Parallel()
	secret := "test-secret-with-enough-entropy"
	hash := platform.HashEmailCode(secret, "user@example.com", "login", "123456")
	if len(hash) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(hash))
	}
	if !platform.VerifyEmailCode(secret, "user@example.com", "login", "123456", hash) {
		t.Fatal("valid code rejected")
	}
	if platform.VerifyEmailCode(secret, "user@example.com", "login", "123457", hash) {
		t.Fatal("wrong code accepted")
	}
	if platform.VerifyEmailCode(secret, "other@example.com", "login", "123456", hash) {
		t.Fatal("code accepted for a different address")
	}
	if platform.VerifyEmailCode(secret, "user@example.com", "signup", "123456", hash) {
		t.Fatal("code accepted for a different purpose")
	}
	if platform.VerifyEmailCode("other-secret", "user@example.com", "login", "123456", hash) {
		t.Fatal("code accepted with a different secret")
	}
}

func TestEncryptDecryptEmailCode(t *testing.T) {
	t.Parallel()
	secret := "test-secret-with-enough-entropy"
	sealed, err := platform.EncryptEmailCode(secret, "004217")
	if err != nil {
		t.Fatalf("EncryptEmailCode: %v", err)
	}
	if strings.Contains(sealed, "004217") {
		t.Fatal("ciphertext leaks the plaintext")
	}
	opened, err := platform.DecryptEmailCode(secret, sealed)
	if err != nil {
		t.Fatalf("DecryptEmailCode: %v", err)
	}
	if opened != "004217" {
		t.Fatalf("got %q, want 004217", opened)
	}
	if _, err := platform.DecryptEmailCode("wrong-secret", sealed); err == nil {
		t.Fatal("decryption with the wrong key succeeded")
	}
	if _, err := platform.DecryptEmailCode(secret, sealed+".tampered"); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestRenderEmailCode(t *testing.T) {
	t.Parallel()
	subject, html, text, err := platform.RenderEmailCode(platform.EmailCodeData{
		Brand: "GetContact", Code: "123456", Purpose: "login",
		Email: "user@example.com", TTLMinutes: 10, SupportEmail: "support@example.com",
	})
	if err != nil {
		t.Fatalf("RenderEmailCode: %v", err)
	}
	if !strings.Contains(subject, "GetContact") {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{"GetContact", "1", "2", "3", "4", "5", "6", "user@example.com", "10 минут"} {
		if !strings.Contains(html, want) {
			t.Fatalf("html is missing %q", want)
		}
	}
	for _, want := range []string{"123456", "10 минут", "user@example.com"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text is missing %q", want)
		}
	}
}
