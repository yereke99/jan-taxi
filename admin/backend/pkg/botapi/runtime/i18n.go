package runtime

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// DefaultLanguage is the bots' default language (Kazakh).
const DefaultLanguage = "kk"

// Languages are the supported languages in display order.
var Languages = []string{"kk", "ru", "en"}

// Lang normalises a stored language code; anything unknown becomes Kazakh.
func Lang(lang string) string {
	if slices.Contains(Languages, lang) {
		return lang
	}
	return DefaultLanguage
}

// Catalog holds the bot texts: language → key → text. Texts are Telegram
// HTML; arguments passed to T must already be escaped (see Esc).
type Catalog map[string]map[string]string

// T returns the text for key in lang (falling back to Kazakh), formatted with
// args when given. A missing key returns the key itself so it is noticed.
func (c Catalog) T(lang, key string, args ...any) string {
	s, ok := c[Lang(lang)][key]
	if !ok {
		if s, ok = c[DefaultLanguage][key]; !ok {
			return key
		}
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

var verbRe = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z%]`)

// Check verifies that every language has exactly the same keys and that each
// text uses the same formatting verbs in every language.
func (c Catalog) Check() error {
	var problems []string
	base := c[DefaultLanguage]
	for _, lang := range Languages {
		m, ok := c[lang]
		if !ok {
			problems = append(problems, "missing language "+lang)
			continue
		}
		for key := range base {
			if _, ok := m[key]; !ok {
				problems = append(problems, fmt.Sprintf("%s: missing %q", lang, key))
			}
		}
		for key, text := range m {
			ref, ok := base[key]
			if !ok {
				problems = append(problems, fmt.Sprintf("%s: unknown key %q", lang, key))
				continue
			}
			if strings.TrimSpace(text) == "" {
				problems = append(problems, fmt.Sprintf("%s: empty %q", lang, key))
			}
			if a, b := verbs(ref), verbs(text); !slices.Equal(a, b) {
				problems = append(problems, fmt.Sprintf("%s: %q uses %v, kk uses %v", lang, key, b, a))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("catalog: %s", strings.Join(problems, "; "))
}

func verbs(s string) []string {
	out := verbRe.FindAllString(s, -1)
	sort.Strings(out)
	return out
}
