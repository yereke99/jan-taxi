// Package textutil normalises user-supplied free text (order comments, cargo
// descriptions, rating comments) and enforces length limits counted in Unicode
// code points after NFC normalisation, the same rule the Mini Apps apply with
// [...s.normalize('NFC')].length.
package textutil

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxFreeText is the default limit for free-text fields (ARCHITECTURE §11).
const MaxFreeText = 120

var (
	// ErrTooLong means the cleaned text exceeds the limit.
	ErrTooLong = errors.New("text too long")
	// ErrEmpty means the cleaned text is empty but a value is required.
	ErrEmpty = errors.New("text required")
)

// Clean normalises s without enforcing a length limit:
//   - invalid UTF-8 bytes are dropped and the text is NFC-normalised;
//   - line breaks, tabs and every Unicode space become a plain space;
//   - control characters (Cc), bidi controls (U+061C, U+200E, U+200F,
//     U+202A–U+202E, U+2066–U+2069) and invisible characters (U+00AD, U+180E,
//     U+200B, U+2060–U+2064, U+FEFF) are removed;
//   - runs of spaces collapse to one and the result is trimmed.
//
// U+200C/U+200D (needed by emoji sequences and some scripts), variation
// selectors and emoji tag characters are kept.
func Clean(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = norm.NFC.String(s)

	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		switch {
		case isSpace(r):
			pendingSpace = b.Len() > 0
			continue
		case dropped(r):
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	// Removing characters can leave a combining mark next to a new base
	// character, so normalise once more.
	return norm.NFC.String(b.String())
}

// CleanText cleans s and checks that it has at most max code points. A
// non-positive max disables the check. On error the returned string is empty.
func CleanText(s string, max int) (string, error) {
	c := Clean(s)
	if max > 0 && utf8.RuneCountInString(c) > max {
		return "", ErrTooLong
	}
	return c, nil
}

// CleanRequired is CleanText that also rejects text that is empty after cleaning.
func CleanRequired(s string, max int) (string, error) {
	c, err := CleanText(s, max)
	if err != nil {
		return "", err
	}
	if c == "" {
		return "", ErrEmpty
	}
	return c, nil
}

// CleanOptional cleans an optional field: nil or blank input yields nil.
func CleanOptional(s *string, max int) (*string, error) {
	if s == nil {
		return nil, nil
	}
	c, err := CleanText(*s, max)
	if err != nil || c == "" {
		return nil, err
	}
	return &c, nil
}

// Len returns the number of code points of s after NFC normalisation.
func Len(s string) int { return utf8.RuneCountInString(norm.NFC.String(s)) }

// Reason maps ErrTooLong/ErrEmpty to the field reason codes used in
// validation_failed responses ("too_long", "required"); other errors map to "invalid".
func Reason(err error) string {
	switch {
	case errors.Is(err, ErrTooLong):
		return "too_long"
	case errors.Is(err, ErrEmpty):
		return "required"
	default:
		return "invalid"
	}
}

func isSpace(r rune) bool {
	// unicode.IsSpace covers \t \n \v \f \r, U+0085, U+00A0, U+1680,
	// U+2000–U+200A, U+2028, U+2029, U+202F, U+205F and U+3000.
	return unicode.IsSpace(r)
}

func dropped(r rune) bool {
	switch {
	case unicode.Is(unicode.Cc, r):
		return true
	case r == 0x061C, r == 0x200E, r == 0x200F:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0x00AD, r == 0x180E, r == 0x200B, r == 0xFEFF:
		return true
	case r >= 0x2060 && r <= 0x2064:
		return true
	}
	return false
}
