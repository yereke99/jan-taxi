// Package iin validates and masks Kazakhstan individual identification
// numbers (ЖСН / ИИН): twelve digits, the last of which is a check digit.
//
// The check digit is the weighted sum of the first eleven digits modulo 11
// with weights 1..11; when that gives 10, the sum is taken again with weights
// 3..11, 1, 2, and a second 10 means the number is never issued.
package iin

import (
	"errors"
	"strings"
	"unicode"
)

// Length is the number of digits of an IIN.
const Length = 12

// Errors returned by Normalize.
var (
	// ErrFormat means the input is not twelve digits.
	ErrFormat = errors.New("iin: must be 12 digits")
	// ErrChecksum means the twelve digits fail the check digit.
	ErrChecksum = errors.New("iin: check digit mismatch")
)

// Normalize drops white space (drivers sometimes type the number in groups)
// and returns the twelve digits, or ErrFormat / ErrChecksum. Leading zeros
// are kept: an IIN is a string, never a number.
func Normalize(s string) (string, error) {
	v := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	if !digits(v) {
		return "", ErrFormat
	}
	if !checksum(v) {
		return "", ErrChecksum
	}
	return v, nil
}

// Valid reports whether s is exactly twelve digits with a valid check digit.
func Valid(s string) bool { return digits(s) && checksum(s) }

// Mask hides all but the last four characters, e.g. "********1234"; an empty
// value stays empty.
func Mask(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	if n <= 4 {
		return strings.Repeat("*", n)
	}
	return strings.Repeat("*", n-4) + s[n-4:]
}

func digits(s string) bool {
	if len(s) != Length {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// checksum expects twelve ASCII digits.
func checksum(s string) bool {
	c := weighted(s, func(i int) int { return i + 1 })
	if c == 10 {
		c = weighted(s, func(i int) int { return (i+2)%11 + 1 })
	}
	return c != 10 && c == int(s[11]-'0')
}

func weighted(s string, weight func(int) int) int {
	sum := 0
	for i := 0; i < 11; i++ {
		sum += int(s[i]-'0') * weight(i)
	}
	return sum % 11
}
