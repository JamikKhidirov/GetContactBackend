package platform_test

import (
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
		{"russian formatted", "+7 (999) 123-45-67", "+79991234567", false},
		{"russian local 8", "8 999 123 45 67", "+79991234567", false},
		{"russian ten digits", "9991234567", "+79991234567", false},
		{"international zeros", "00 44 20 7946 0958", "+442079460958", false},
		{"dotted", "+1.202.555.0100", "+12025550100", false},
		{"empty", "", "", true},
		{"letters", "+7 999 abc", "", true},
		{"too short", "+12345", "", true},
		{"leading zero", "0123456789", "", true},
		{"too long", "+1234567890123456", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := platform.NormalizePhone(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMaskPhone(t *testing.T) {
	if got := platform.MaskPhone("+79991234567"); got != "*******4567" {
		t.Fatalf("unexpected mask: %q", got)
	}
	if got := platform.MaskPhone("123"); got != "****" {
		t.Fatalf("unexpected short mask: %q", got)
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
