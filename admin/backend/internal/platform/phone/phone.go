// Package phone normalises Kazakhstan mobile numbers to E.164 (+77XXXXXXXXX).
//
// Kazakhstan shares country code +7 with Russia; Kazakh numbers start with 7
// after the country code. Only mobile ranges are accepted, per the KZ mobile
// pattern in Google libphonenumber metadata (v9.0.41):
// 7(0[0-25-8]|47|6[0-4]|7[15-8]|85)XXXXXXX, i.e. prefixes 700–702, 705–708,
// 747, 760–764, 771, 775–778 and 785. Number portability means the prefix does
// not identify the operator.
package phone

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrInvalid means the input is not a phone number in an accepted format.
	ErrInvalid = errors.New("invalid phone number")
	// ErrNotMobile means the input is a well-formed +7 number outside the
	// Kazakhstan mobile ranges (Russian numbers, KZ landlines, VoIP 751).
	ErrNotMobile = errors.New("not a Kazakhstan mobile number")
)

var (
	mobileRe = regexp.MustCompile(`^7(?:70[0-25-8]|747|76[0-4]|77[15-8]|785)[0-9]{7}$`)
	e164Re   = regexp.MustCompile(`^\+77[0-9]{9}$`)
)

// Normalize accepts "8 7xx xxx xx xx", "+7 7xx xxx xx xx", "7 7xx…" and the
// 10-digit national form "7xx xxx xx xx", with optional spaces, dashes, dots
// and parentheses, and returns "+77XXXXXXXXX".
func Normalize(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 32 {
		return "", ErrInvalid
	}
	plus := false
	digits := make([]byte, 0, 12)
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits = append(digits, byte(r))
		case r == '+':
			if i != 0 {
				return "", ErrInvalid
			}
			plus = true
		case r == ' ', r == '-', r == '(', r == ')', r == '.', r == '\u00a0':
		default:
			return "", ErrInvalid
		}
	}

	var national string // 11 digits starting with the country code 7
	switch {
	case plus:
		if len(digits) != 11 || digits[0] != '7' {
			return "", ErrInvalid
		}
		national = string(digits)
	case len(digits) == 11 && (digits[0] == '8' || digits[0] == '7'):
		national = "7" + string(digits[1:])
	case len(digits) == 10 && digits[0] == '7':
		national = "7" + string(digits)
	default:
		return "", ErrInvalid
	}
	if !mobileRe.MatchString(national) {
		return "", ErrNotMobile
	}
	return "+" + national, nil
}

// Valid reports whether s is already a normalised KZ mobile number.
func Valid(s string) bool { return e164Re.MatchString(s) && mobileRe.MatchString(s[1:]) }

// Format renders a normalised number for display: "+7 701 234 56 78". Other
// input is returned unchanged.
func Format(e164 string) string {
	if !e164Re.MatchString(e164) {
		return e164
	}
	d := e164[2:] // 10 national digits
	return "+7 " + d[0:3] + " " + d[3:6] + " " + d[6:8] + " " + d[8:10]
}

// Mask hides the middle digits for display to other parties and in admin
// lists: "+7 701 *** ** 78". Input that is not a valid number yields "".
func Mask(s string) string {
	n, err := Normalize(s)
	if err != nil {
		return ""
	}
	d := n[2:]
	return "+7 " + d[0:3] + " *** ** " + d[8:10]
}
