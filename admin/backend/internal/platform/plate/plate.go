// Package plate normalises and validates Kazakhstan vehicle registration plates
// (ГРНЗ, МВД order No. 1040 of 19.12.2015).
//
// Accepted formats, after upper-casing, removing separators and mapping
// Cyrillic look-alike letters to Latin:
//
//	current (since 2012): NNN LL(L) RR  e.g. 123 ABC 04 → "123ABC04"
//	                      RR is the region code 01–20 (04 = Aktobe region)
//	legacy (1993–2012):   L NNN LL(L)   e.g. D 123 ABC → "D123ABC"
//	                      the first letter is the region (D = Aktobe region)
//
// Decision on legacy plates: plates issued in 1993–2012 remain legally valid
// and replacement is not mandatory, so drivers in small towns such as Embi still
// drive with them. Rejecting them would block legitimate drivers, so legacy
// plates are accepted and reported with Kind == KindLegacy, which the admin
// panel shows next to the plate for a later check.
// Diplomatic, foreign-owner and special-service series use other layouts and
// are rejected (they are not eligible for taxi work).
package plate

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Kind tells which plate generation a normalised plate belongs to.
type Kind string

const (
	KindCurrent Kind = "current"
	KindLegacy  Kind = "legacy"
)

// ErrInvalid means the input is not a recognised Kazakhstan plate.
var ErrInvalid = errors.New("invalid plate number")

// Plate is a parsed plate.
type Plate struct {
	Normalized string // canonical form stored in vehicles.plate_normalized, e.g. "123ABC04"
	Kind       Kind
	Region     string // "04" for current plates, "D" for legacy plates
}

var (
	currentRe = regexp.MustCompile(`^([0-9]{3})([A-Z]{2,3})(0[1-9]|1[0-9]|20)$`)
	legacyRe  = regexp.MustCompile(`^([A-Z])([0-9]{3})([A-Z]{2,3})$`)
)

// lookalikes maps Cyrillic capitals that are visually identical to the Latin
// letters used on plates. Plates carry Latin letters only, but drivers often
// type them on a Russian or Kazakh keyboard.
var lookalikes = map[rune]rune{
	'А': 'A', 'В': 'B', 'Е': 'E', 'К': 'K', 'М': 'M', 'Н': 'H', 'О': 'O',
	'Р': 'P', 'С': 'C', 'Т': 'T', 'У': 'Y', 'Х': 'X', 'Ү': 'Y', 'І': 'I',
	'Ј': 'J', 'Ѕ': 'S', 'Ԛ': 'Q', 'Ԝ': 'W',
}

// Normalize returns the canonical plate (e.g. "123ABC04") or ErrInvalid.
func Normalize(s string) (string, error) {
	p, err := Parse(s)
	if err != nil {
		return "", err
	}
	return p.Normalized, nil
}

// Parse normalises s and identifies its format.
func Parse(s string) (Plate, error) {
	if utf8.RuneCountInString(s) > 32 {
		return Plate{}, ErrInvalid
	}
	canon, ok := canonical(s)
	if !ok || canon == "" {
		return Plate{}, ErrInvalid
	}
	if p, ok := match(canon); ok {
		return p, nil
	}
	// Some drivers copy the "KZ" country mark printed on the plate.
	if rest, found := strings.CutPrefix(canon, "KZ"); found {
		if p, ok := match(rest); ok {
			return p, nil
		}
	}
	return Plate{}, ErrInvalid
}

func match(s string) (Plate, bool) {
	if m := currentRe.FindStringSubmatch(s); m != nil {
		return Plate{Normalized: s, Kind: KindCurrent, Region: m[3]}, true
	}
	if m := legacyRe.FindStringSubmatch(s); m != nil {
		return Plate{Normalized: s, Kind: KindLegacy, Region: m[1]}, true
	}
	return Plate{}, false
}

// canonical upper-cases s, folds full-width forms (NFKC), maps look-alikes,
// and drops separators. It fails on any other character.
func canonical(s string) (string, bool) {
	s = strings.ToUpper(norm.NFKC.String(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if l, ok := lookalikes[r]; ok {
			r = l
		}
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case isSeparator(r):
		default:
			return "", false
		}
	}
	return b.String(), true
}

func isSeparator(r rune) bool {
	switch r {
	case ' ', '\t', '-', '.', '_', '|', '/', '\u00a0', '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2212':
		return true
	}
	return false
}

// Format renders a canonical plate for display: "123 ABC 04" or "D 123 ABC".
// Input that is not a canonical plate is returned unchanged.
func Format(normalized string) string {
	if m := currentRe.FindStringSubmatch(normalized); m != nil {
		return m[1] + " " + m[2] + " " + m[3]
	}
	if m := legacyRe.FindStringSubmatch(normalized); m != nil {
		return m[1] + " " + m[2] + " " + m[3]
	}
	return normalized
}
