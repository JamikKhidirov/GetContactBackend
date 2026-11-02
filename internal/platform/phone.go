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

const (
	// minPhoneDigits is the shortest number accepted by the API. E.164 does not
	// define a hard minimum, but national numbers shorter than eight digits are
	// almost always internal extensions or typos for a caller-ID service.
	minPhoneDigits = 8
	// maxPhoneDigits is the E.164 upper bound: country code + national number.
	maxPhoneDigits = 15
	// maxPhoneInput bounds the raw input. A valid number never needs more than
	// 15 digits plus separators, so rejecting oversized strings early keeps the
	// parser cheap and predictable.
	maxPhoneInput = 64
	// maxRawDigits allows an international "00" prefix in front of a 15-digit
	// E.164 number while the parser is still collecting digits.
	maxRawDigits = maxPhoneDigits + 2
)

// NormalizePhone converts a human-formatted phone number into a compact E.164
// representation: a leading '+' followed by 8..15 digits.
//
// Accepted syntax:
//   - an optional single leading '+' (ASCII or full-width), or the ITU
//     international access prefix "00";
//   - ASCII digits and full-width digits (１２３…);
//   - common separators: any Unicode space, any dash punctuation (including
//     '-', '–', '—', '−'), parentheses, brackets, '.', '/', ',' and '_'.
//
// Everything else (letters, extension markers, a second '+' inside the number)
// is rejected instead of being silently discarded.
//
// Russian local formats are recognised when no explicit international prefix
// is present:
//   - 11 digits starting with the trunk prefix 8 → +7…;
//   - 10 digits starting with 3, 4, 8 or 9 → +7….
//
// An explicit international prefix disables both heuristics, so a valid foreign
// number such as "+47 12345678" is never rewritten into a Russian one.
func NormalizePhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	// Zero-width characters often survive copy/paste from web pages and chat
	// clients; they are not Unicode whitespace, so trim them explicitly.
	raw = strings.Trim(raw, "\ufeff\u200b")
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxPhoneInput {
		return "", ErrInvalidPhone
	}

	var digits strings.Builder
	digits.Grow(maxRawDigits)

	plus := false
	for i, r := range raw {
		switch {
		case r == '+' || r == '＋':
			// '+' is only valid once, before any other character. A leading '+'
			// is safe even when followed by separators ("+ 7 999 …").
			if i != 0 || plus {
				return "", ErrInvalidPhone
			}
			plus = true
		case r >= '0' && r <= '9':
			digits.WriteByte(byte(r))
		case r >= '０' && r <= '９':
			digits.WriteByte(byte(r-'０') + '0')
		case isPhoneSeparator(r):
			// Formatting only; ignore.
		default:
			return "", ErrInvalidPhone
		}
		if digits.Len() > maxRawDigits {
			return "", ErrInvalidPhone
		}
	}

	number := digits.String()
	if number == "" {
		return "", ErrInvalidPhone
	}

	// "00" is the international access code and is equivalent to a leading '+'.
	// Cut it exactly once and guard the empty result: a bare "00" must not
	// reach the indexing below.
	trimmed, hadDoubleZero := strings.CutPrefix(number, "00")
	if trimmed == "" {
		return "", ErrInvalidPhone
	}
	number = trimmed
	international := plus || hadDoubleZero

	// Russian trunk prefix: 8XXXXXXXXXX. This must only apply to bare local
	// input. With an explicit "+" or "00" an 11-digit number may be a valid
	// foreign number such as "+81 3 1234 5678" (Japan), so rewriting it would
	// corrupt the value.
	if !international && len(number) == 11 && number[0] == '8' {
		number = "7" + number[1:]
	}

	// Bare Russian national numbers are accepted only without an explicit
	// international prefix and only for plausible national prefixes. This keeps
	// "+47 12345678" (10 digits) intact while still supporting "9991234567".
	if len(number) == 10 && !international && isRussianNationalPrefix(number[0]) {
		number = "7" + number
	}

	if len(number) < minPhoneDigits || len(number) > maxPhoneDigits || number[0] == '0' {
		return "", ErrInvalidPhone
	}

	// +7 is shared by Russia and Kazakhstan. Their national numbers start with
	// 1, 2, 5 or 6 only for unassigned ranges, so reject obvious garbage such as
	// "+7 000 …" or an 11-digit string that is not a phone number at all.
	if number[0] == '7' && len(number) == 11 && !isRussianNationalPrefix(number[1]) {
		return "", ErrInvalidPhone
	}

	return "+" + number, nil
}

// isRussianNationalPrefix reports whether the first national digit belongs to
// an assigned Russian or Kazakh numbering range: 3xx/4xx/8xx/9xx in Russia and
// 7xx in Kazakhstan. 1xx, 2xx, 5xx and 6xx are unassigned.
func isRussianNationalPrefix(digit byte) bool {
	switch digit {
	case '3', '4', '7', '8', '9':
		return true
	default:
		return false
	}
}

// isPhoneSeparator lists characters that may appear in formatted phone numbers.
func isPhoneSeparator(r rune) bool {
	if unicode.IsSpace(r) || unicode.Is(unicode.Pd, r) {
		// Pd covers hyphen-minus, non-breaking hyphen, figure dash, en dash,
		// em dash and the other Unicode dash punctuation characters.
		return true
	}
	switch r {
	case '−', // U+2212 MINUS SIGN (math symbol, not Pd)
		'(', ')', '[', ']', '.', '/', ',', '_':
		return true
	default:
		return false
	}
}

// PhoneDigits returns only the ASCII digits of a phone number (for fuzzy search).
func PhoneDigits(phone string) string {
	var b strings.Builder
	b.Grow(len(phone))
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteByte(byte(r))
		}
	}
	return b.String()
}

// MaskPhone hides all but the last four digits for logs and metrics labels.
// Very short values are fully masked so the mask never reveals their length.
func MaskPhone(phone string) string {
	digits := PhoneDigits(phone)
	if len(digits) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(digits)-4) + digits[len(digits)-4:]
}
