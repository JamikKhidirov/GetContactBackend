// Package platform contains small, dependency-light building blocks shared by
// services and transports: phone normalisation, password hashing, JWTs, logging.
package platform

import (
	"errors"
	"strings"
	"unicode"
)

// ErrInvalidPhone is returned when a phone number cannot be normalised.
var ErrInvalidPhone = errors.New("invalid phone number")

// NormalizePhone converts a human-formatted phone number to a compact E.164-like
// representation. It is deliberately permissive about separators and accepts
// Russian local formats (8XXXXXXXXXX and 10-digit numbers).
func NormalizePhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidPhone
	}

	var b strings.Builder
	for i, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '-', r == '(', r == ')', r == '.', r == '/':
			// separators are ignored
		default:
			return "", ErrInvalidPhone
		}
	}

	digits := strings.TrimPrefix(b.String(), "+")
	if strings.HasPrefix(digits, "00") {
		digits = strings.TrimPrefix(digits, "00")
	}
	switch {
	case len(digits) == 11 && strings.HasPrefix(digits, "8"):
		digits = "7" + digits[1:]
	case len(digits) == 10 && digits[0] != '0':
		digits = "7" + digits
	}

	if len(digits) < 8 || len(digits) > 15 || strings.HasPrefix(digits, "0") {
		return "", ErrInvalidPhone
	}
	return "+" + digits, nil
}

// PhoneDigits returns only the digits of a phone number (for fuzzy search).
func PhoneDigits(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MaskPhone hides all but the last four digits for logs and metrics labels.
func MaskPhone(phone string) string {
	digits := PhoneDigits(phone)
	if len(digits) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(digits)-4) + digits[len(digits)-4:]
}
