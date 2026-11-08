package platform

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// NormalizeEmail lower-cases and trims an address, then validates it. Email is
// treated as a required, case-insensitive identifier.
func NormalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", fmt.Errorf("email is required")
	}
	if err := ValidateEmail(email); err != nil {
		return "", err
	}
	return email, nil
}

// GenerateNumericCode returns a cryptographically random code of the given
// length. Leading zeroes are preserved (the code is a string, not a number).
func GenerateNumericCode(length int) (string, error) {
	if length < 4 || length > 12 {
		return "", fmt.Errorf("code length must be between 4 and 12")
	}
	ten := big.NewInt(10)
	var b strings.Builder
	b.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, ten)
		if err != nil {
			return "", fmt.Errorf("generate code: %w", err)
		}
		b.WriteByte(byte('0' + n.Int64()))
	}
	return b.String(), nil
}

// HashEmailCode derives the value persisted for OTP verification. The secret is
// required so a database dump alone cannot brute-force the six-digit code.
// Email and purpose are bound into the MAC, preventing a code issued for one
// address or use case from being replayed for another.
func HashEmailCode(secret, email, purpose, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(email))
	mac.Write([]byte{0})
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyEmailCode compares a candidate code with a stored HMAC in constant time.
func VerifyEmailCode(secret, email, purpose, code, expected string) bool {
	computed := HashEmailCode(secret, email, purpose, code)
	return hmac.Equal([]byte(computed), []byte(expected))
}

// EncryptEmailCode seals a one-time code with AES-256-GCM. Only the delivery
// worker decrypts it immediately before rendering the email, so the plaintext
// never lands in logs, Kafka payloads or the outbox.
func EncryptEmailCode(secret, code string) (string, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(code), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// DecryptEmailCode opens a value produced by EncryptEmailCode.
func DecryptEmailCode(secret, encoded string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext is truncated")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt code: %w", err)
	}
	return string(plain), nil
}

// MaskEmail hides most of the local part for safe display in logs/audit views.
func MaskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	local, domain := email[:at], email[at:]
	if len(local) <= 2 {
		return strings.Repeat("*", len(local)) + domain
	}
	return local[:1] + strings.Repeat("*", len(local)-2) + local[len(local)-1:] + domain
}
