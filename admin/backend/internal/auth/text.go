package auth

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// cleanName trims s, drops control and format characters (incl. bidi overrides),
// collapses whitespace and truncates to max code points. Used for names that come
// from Telegram or admin input and end up in UIs and notifications.
func cleanName(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	b.Grow(len(s))
	n := 0
	space := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if space && n > 0 {
			if n+1 >= max {
				break
			}
			b.WriteByte(' ')
			n++
		}
		space = false
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
