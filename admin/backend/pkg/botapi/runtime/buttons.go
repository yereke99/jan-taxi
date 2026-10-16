package runtime

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Matcher maps reply-keyboard button texts (in every language) to actions.
// Matching ignores emoji, surrounding punctuation, spacing and letter case,
// so a stale keyboard in another language or a typed label still works.
type Matcher struct {
	m map[string]string
}

// NewMatcher returns an empty Matcher.
func NewMatcher() *Matcher { return &Matcher{m: map[string]string{}} }

// Add registers labels for action. It returns an error when a label already
// maps to a different action (two buttons would be indistinguishable).
func (m *Matcher) Add(action string, labels ...string) error {
	for _, l := range labels {
		key := NormalizeText(l)
		if key == "" {
			return fmt.Errorf("matcher: label %q has no text", l)
		}
		if prev, ok := m.m[key]; ok && prev != action {
			return fmt.Errorf("matcher: %q is used by %s and %s", l, prev, action)
		}
		m.m[key] = action
	}
	return nil
}

// Match returns the action of a button text.
func (m *Matcher) Match(text string) (string, bool) {
	a, ok := m.m[NormalizeText(text)]
	return a, ok
}

// NormalizeText reduces a label to its words: NFC, lower case, without
// leading/trailing emoji or punctuation and with single spaces.
func NormalizeText(s string) string {
	s = norm.NFC.String(s)
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	s = strings.TrimFunc(s, func(r rune) bool { return !isWord(r) })
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
