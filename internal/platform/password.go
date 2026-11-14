package platform

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ArgonParams controls the memory-hard password hashing cost.
type ArgonParams struct {
	Memory     uint32
	Iterations uint32
	Threads    uint8
	KeyLen     uint32
}

// DefaultArgonParams matches OWASP's argon2id recommendation (64 MiB, t=3, p=2).
var DefaultArgonParams = ArgonParams{Memory: 64 * 1024, Iterations: 3, Threads: 2, KeyLen: 32}

// HashPassword returns a PHC-formatted argon2id hash: $argon2id$v=19$m=...,t=...,p=...$salt$hash.
func HashPassword(password string, p ArgonParams) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword performs a constant-time comparison against a PHC argon2id hash.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

// NewOpaqueToken returns a URL-safe random token and its SHA-256 hash. Only the
// hash is persisted, so a database leak cannot be replayed.
func NewOpaqueToken(bytesLen int) (raw, hash string, err error) {
	buf := make([]byte, bytesLen)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashOpaqueToken(raw), nil
}

// HashOpaqueToken hashes a refresh token with SHA-256 (hex encoded).
func HashOpaqueToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ParseIntDefault is a tiny helper for query parameters.
func ParseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
