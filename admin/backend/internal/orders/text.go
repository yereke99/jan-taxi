package orders

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

// containsBlocked reports whether text contains one of the blocked keywords
// at the start of a word, ignoring case and Unicode normalisation form. Word
// starts (rather than any substring) keep ordinary words that merely contain
// a short keyword from being rejected while still catching inflected forms
// ("оружие" → "оружием", "drug" → "drugs").
func containsBlocked(text string, keywords []string) bool {
	t := fold(text)
	if t == "" {
		return false
	}
	for _, kw := range keywords {
		k := fold(kw)
		if k == "" {
			continue
		}
		for i := 0; i < len(t); {
			j := strings.Index(t[i:], k)
			if j < 0 {
				break
			}
			at := i + j
			if at == 0 || !isWordRune(lastRune(t[:at])) {
				return true
			}
			i = at + len(k)
		}
	}
	return false
}

func fold(s string) string {
	return strings.ToLower(norm.NFC.String(textutil.Clean(s)))
}

func lastRune(s string) rune {
	r := []rune(s)
	if len(r) == 0 {
		return 0
	}
	return r[len(r)-1]
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

// cleanField applies the free-text rules to an optional field and records a
// field reason on failure. Blank input yields nil.
func cleanField(fields map[string]string, name string, v *string, max int) *string {
	c, err := textutil.CleanOptional(v, max)
	if err != nil {
		fields[name] = textutil.Reason(err)
		return nil
	}
	return c
}

// cleanRequiredField is cleanField for a required value.
func cleanRequiredField(fields map[string]string, name string, v *string, max int) string {
	if v == nil {
		fields[name] = "required"
		return ""
	}
	c, err := textutil.CleanRequired(*v, max)
	if err != nil {
		fields[name] = textutil.Reason(err)
		return ""
	}
	return c
}

// decodeOptionalJSON decodes a JSON body into dst when the request has one; an
// absent or empty body leaves dst untouched.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	br := bufio.NewReader(r.Body)
	if _, err := br.Peek(1); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return httpx.BadRequest("cannot read request body").WithCause(err)
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{br, r.Body}
	return httpx.DecodeJSON(w, r, dst)
}
