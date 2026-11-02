package platform_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/getcontact/backend/internal/platform"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		// Russian local formats.
		{"russian formatted", "+7 (999) 123-45-67", "+79991234567", false},
		{"russian local 8", "8 999 123 45 67", "+79991234567", false},
		{"russian local 8 compact", "89991234567", "+79991234567", false},
		{"russian ten digits", "9991234567", "+79991234567", false},
		{"russian ten digits city", "495 123-45-67", "+74951234567", false},
		{"russian toll free", "8 (800) 555-35-35", "+78005553535", false},
		{"kazakhstan mobile", "+7 701 123 45 67", "+77011234567", false},
		{"plus then separator", "+ 7 999 123 45 67", "+79991234567", false},
		{"brackets", "8[999]1234567", "+79991234567", false},
		{"slashes", "8/999/123/45/67", "+79991234567", false},
		{"commas", "8,999,123,45,67", "+79991234567", false},
		{"underscores", "8_999_123_45_67", "+79991234567", false},

		// International formats. Explicit prefixes must disable the Russian
		// 10-digit heuristic, otherwise +47 12345678 would become +74712345678.
		{"international zeros", "00 44 20 7946 0958", "+442079460958", false},
		{"international zeros with plus", "+00 44 20 7946 0958", "+442079460958", false},
		{"international ten digits", "+47 12345678", "+4712345678", false},
		{"international ten digits via 00", "0049 30 123456", "+4930123456", false},
		{"dotted", "+1.202.555.0100", "+12025550100", false},
		{"us formatted", "+1 (202) 555-0100", "+12025550100", false},
		{"uk formatted", "+44 20 7946 0958", "+442079460958", false},
		{"germany ten digits", "+49 30 123456", "+4930123456", false},
		{"japan", "+81 3 1234 5678", "+81312345678", false},
		{"vietnam", "+84 912 345 678", "+84912345678", false},
		{"south korea", "+82 2 1234 5678", "+82212345678", false},
		{"china", "+86 10 1234 5678", "+861012345678", false},

		// Unicode digits and punctuation are common in copy-pasted numbers.
		{"full width digits", "＋７９９９１２３４５６７", "+79991234567", false},
		{"full width digits plus trunk", "８９９９１２３４５６７", "+79991234567", false},
		{"en dash", "8–999–123–45–67", "+79991234567", false},
		{"em dash", "8—999—123—45—67", "+79991234567", false},
		{"minus sign", "8−999−123−45−67", "+79991234567", false},
		{"non breaking space", "8\u00a0999\u00a0123\u00a045\u00a067", "+79991234567", false},
		{"byte order mark", "\ufeff+79991234567", "+79991234567", false},
		{"zero width space", "\u200b8 999 123 45 67\u200b", "+79991234567", false},

		// "00" edge cases: a bare prefix must be rejected without panicking.
		{"bare double zero", "00", "", true},
		{"triple zero", "000", "", true},
		{"zero prefix only", "00 00 00 00 00 00", "", true},
		{"triple zero before number", "00079991234567", "", true},
		{"double zero before russian", "0079991234567", "+79991234567", false},

		// Invalid input.
		{"empty", "", "", true},
		{"spaces only", "   ", "", true},
		{"plus only", "+", "", true},
		{"letters", "+7 999 abc", "", true},
		{"extension text", "8 999 123 45 67 ext 12", "", true},
		{"double plus", "++79991234567", "", true},
		{"plus inside", "+7+9991234567", "", true},
		{"too short", "+12345", "", true},
		{"leading zero", "0123456789", "", true},
		{"too long", "+1234567890123456", "", true},
		{"invalid russian range", "+7 123 456 78 90", "", true},
		{"invalid trunk number", "8 123 456 78 90", "", true},
		{"too long input", "+" + strings.Repeat("9", 80), "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := platform.NormalizePhone(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if !errors.Is(err, platform.ErrInvalidPhone) {
					t.Fatalf("expected ErrInvalidPhone, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}

			// Normalisation must be idempotent for every accepted value.
			again, err := platform.NormalizePhone(got)
			if err != nil {
				t.Fatalf("normalising %q again failed: %v", got, err)
			}
			if again != got {
				t.Fatalf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestNormalizePhoneNeverPanics(t *testing.T) {
	inputs := []string{
		"", "+", "++", "00", "000", "+00", "00+", "()", "---", "  ",
		strings.Repeat("0", 100), strings.Repeat("+", 100), strings.Repeat("(", 64),
		"+7", "8", "00" + strings.Repeat("9", 15), "+" + strings.Repeat("9", 15),
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("NormalizePhone(%q) panicked: %v", in, r)
				}
			}()
			out, err := platform.NormalizePhone(in)
			if err == nil {
				if !strings.HasPrefix(out, "+") {
					t.Fatalf("NormalizePhone(%q) = %q, missing leading +", in, out)
				}
				if n := len(strings.TrimPrefix(out, "+")); n < 8 || n > 15 {
					t.Fatalf("NormalizePhone(%q) = %q, %d digits", in, out, n)
				}
			}
		}()
	}
}

func TestPhoneDigits(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"+7 (999) 123-45-67", "79991234567"},
		{"8 999 123 45 67", "89991234567"},
		{"no digits", ""},
		{"１２３", ""}, // full-width digits are not ASCII
	}
	for _, tt := range tests {
		if got := platform.PhoneDigits(tt.input); got != tt.want {
			t.Fatalf("PhoneDigits(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"+79991234567", "*******4567"},
		{"+7 (999) 123-45-67", "*******4567"},
		{"12345", "*2345"},
		{"1234", "****"},
		{"123", "****"},
		{"", "****"},
		{"no digits", "****"},
	}
	for _, tt := range tests {
		if got := platform.MaskPhone(tt.input); got != tt.want {
			t.Fatalf("MaskPhone(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"  SPAM  ", "spam", false},
		{"call center", "call center", false},
		{"мошенник", "мошенник", false},
		{"a", "", true},
		{"bad<script>", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := platform.NormalizeTag(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("input %q: expected error, got %q", tt.input, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Fatalf("input %q: got %q, err %v; want %q", tt.input, got, err, tt.want)
		}
	}
}
