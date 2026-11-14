package platform_test

import (
	"strings"
	"testing"

	"github.com/getcontact/backend/internal/platform"
)

func TestPasswordHashAndVerify(t *testing.T) {
	params := platform.ArgonParams{Memory: 8 * 1024, Iterations: 1, Threads: 1, KeyLen: 32}
	hash, err := platform.HashPassword("Secret123", params)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "Secret123" {
		t.Fatal("password stored in clear text")
	}
	if !platform.VerifyPassword("Secret123", hash) {
		t.Fatal("valid password rejected")
	}
	if platform.VerifyPassword("Secret124", hash) {
		t.Fatal("invalid password accepted")
	}
	if platform.VerifyPassword("Secret123", "not-a-hash") {
		t.Fatal("malformed hash accepted")
	}
	if _, err := platform.HashPassword("short", params); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestPasswordPolicyCountsRunes(t *testing.T) {
	// Eight Cyrillic runes are 16 bytes; the byte-based check used to accept
	// this while claiming a minimum of eight characters.
	if err := platform.ValidatePassword("Пароль12"); err != nil {
		t.Fatalf("valid unicode password rejected: %v", err)
	}
	if err := platform.ValidatePassword("Пар1"); err == nil {
		t.Fatal("short unicode password accepted")
	}
	if err := platform.ValidatePassword(strings.Repeat("а", 129) + "1"); err == nil {
		t.Fatal("too many runes accepted")
	}
}

func TestOpaqueToken(t *testing.T) {
	raw, hash, err := platform.NewOpaqueToken(32)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if len(raw) < 40 {
		t.Fatalf("token too short: %d", len(raw))
	}
	if hash != platform.HashOpaqueToken(raw) {
		t.Fatal("hash mismatch")
	}
	raw2, _, _ := platform.NewOpaqueToken(32)
	if raw == raw2 {
		t.Fatal("tokens are not random")
	}
}
