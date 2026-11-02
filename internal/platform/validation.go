package platform

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	tagRe   = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _\-]{1,31}$`)
)

// ValidatePassword enforces the password policy: 8..128 chars, at least one
// letter and one digit.
func ValidatePassword(p string) error {
	if len(p) < 8 || len(p) > 128 {
		return fmt.Errorf("password must be between 8 and 128 characters")
	}
	var letter, digit bool
	for _, r := range p {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			letter = true
		}
	}
	if !letter || !digit {
		return fmt.Errorf("password must contain at least one letter and one digit")
	}
	return nil
}

// ValidateDisplayName checks the public display name length.
func ValidateDisplayName(name string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(name))
	if n < 2 || n > 64 {
		return fmt.Errorf("display_name must be between 2 and 64 characters")
	}
	return nil
}

// ValidateEmail allows an empty email (optional field) or a sane address.
func ValidateEmail(email string) error {
	if email == "" {
		return nil
	}
	if len(email) > 254 || !emailRe.MatchString(email) {
		return fmt.Errorf("email is invalid")
	}
	return nil
}

// NormalizeTag canonicalises community tags: trimmed, lower-cased, single spaces.
func NormalizeTag(tag string) (string, error) {
	tag = strings.ToLower(strings.Join(strings.Fields(tag), " "))
	if !tagRe.MatchString(tag) {
		return "", fmt.Errorf("tag must be 2..32 characters: letters, digits, spaces, '-' or '_'")
	}
	return tag, nil
}

// NormalizePersonName canonicalises a user-contributed name for a phone number.
// It accepts any human script, collapses whitespace and rejects control
// characters and angle brackets so the value stays safe in every renderer.
func NormalizePersonName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	n := utf8.RuneCountInString(name)
	if n < 2 || n > 64 {
		return "", fmt.Errorf("name must be between 2 and 64 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return "", fmt.Errorf("name contains invalid characters")
		}
	}
	return name, nil
}

// SpamCategories is the closed set accepted by POST /numbers/{phone}/spam.
var SpamCategories = map[string]struct{}{
	"fraud":         {},
	"scam":          {},
	"telemarketing": {},
	"robocall":      {},
	"spam":          {},
	"other":         {},
}

// ValidateSpamCategory validates the report category.
func ValidateSpamCategory(c string) error {
	c = strings.ToLower(strings.TrimSpace(c))
	if _, ok := SpamCategories[c]; !ok {
		return fmt.Errorf("category must be one of: fraud, scam, telemarketing, robocall, spam, other")
	}
	return nil
}

// ValidateComment limits free-text comments.
func ValidateComment(c string) error {
	if utf8.RuneCountInString(c) > 500 {
		return fmt.Errorf("comment must be at most 500 characters")
	}
	return nil
}
