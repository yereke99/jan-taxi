package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

// MaxBodyLength is the maximum template body length in characters
// (notification_templates.body CHECK); it leaves room for params within
// Telegram's 4096-character message limit.
const MaxBodyLength = 3500

// maxTextParam bounds each text param so a long address or reason cannot push
// the message over Telegram's limit.
const maxTextParam = 300

const (
	nbsp  = " "
	tenge = "₸"
)

var (
	placeholderRe = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	paramNameRe   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	startParamRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	entityRe      = regexp.MustCompile(`^&(?:lt|gt|amp|quot|#[0-9]{1,7}|#x[0-9a-fA-F]{1,6});`)
	hrefAttrRe    = regexp.MustCompile(`^href="[^"<>]*"$`)
)

// categoryLabels localises the category param.
var categoryLabels = map[string]map[string]string{
	"taxi":   labels("Такси", "Такси", "Taxi"),
	"parcel": labels("Сәлемдеме", "Посылка", "Parcel"),
	"other":  labels("Басқа жеткізу", "Другая доставка", "Other delivery"),
}

// Placeholders returns the param names referenced by body in order of
// appearance, or an error for malformed placeholders ("{{ x }}", "{{", "}}").
func Placeholders(body string) ([]string, error) {
	rest := placeholderRe.ReplaceAllString(body, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return nil, errors.New("malformed placeholder: use {{name}}")
	}
	var names []string
	for _, m := range placeholderRe.FindAllStringSubmatch(body, -1) {
		if !paramNameRe.MatchString(m[1]) {
			return nil, fmt.Errorf("malformed placeholder {{%s}}", m[1])
		}
		names = append(names, m[1])
	}
	return names, nil
}

// ValidateBody checks a (default or admin-supplied) body for this template:
// non-empty, at most MaxBodyLength characters, every placeholder documented, and
// well-formed Telegram HTML.
func (t *Template) ValidateBody(body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("body is empty")
	}
	if utf8.RuneCountInString(body) > MaxBodyLength {
		return fmt.Errorf("body is longer than %d characters", MaxBodyLength)
	}
	names, err := Placeholders(body)
	if err != nil {
		return err
	}
	for _, n := range names {
		if _, ok := t.Param(n); !ok {
			return fmt.Errorf("unknown placeholder {{%s}}", n)
		}
	}
	return ValidateHTML(body)
}

// ValidateParams checks that params match the template: no undocumented keys,
// every required param present and non-empty, values of the right kind.
func (t *Template) ValidateParams(params map[string]any) error {
	for k, v := range params {
		p, ok := t.Param(k)
		if !ok {
			return fmt.Errorf("template %s has no param %q", t.Key, k)
		}
		if isEmpty(v) {
			continue
		}
		if err := checkValue(p, v); err != nil {
			return fmt.Errorf("template %s param %s: %w", t.Key, k, err)
		}
	}
	for _, p := range t.Params {
		if !p.Optional && isEmpty(params[p.Name]) {
			return fmt.Errorf("template %s requires param %s", t.Key, p.Name)
		}
	}
	return nil
}

func checkValue(p Param, v any) error {
	switch p.Kind {
	case KindMoney, KindNumber:
		if _, ok := toInt64(v); !ok {
			return errors.New("must be an integer")
		}
	case KindID:
		s, ok := toString(v)
		if !ok || !startParamRe.MatchString(s) {
			return errors.New("must be an id ([A-Za-z0-9_-], up to 64 characters)")
		}
	case KindCategory:
		s, _ := toString(v)
		if _, ok := categoryLabels[s]; !ok {
			return errors.New("must be taxi, parcel or other")
		}
	default:
		if _, ok := toString(v); !ok {
			return errors.New("must be a string")
		}
	}
	return nil
}

// Message is a rendered notification ready for the Bot API (HTML parse mode).
type Message struct {
	Text   string
	Markup *telegram.InlineKeyboardMarkup
}

// Render renders body (an override or the template default) in lang with
// params. Every param value is HTML-escaped; lines referencing an empty optional
// param are dropped. miniAppURL is the Mini App URL of the sending bot; when it
// is empty, or the start param is not valid, the message has no button.
func (t *Template) Render(lang, body string, params map[string]any, miniAppURL string) (Message, error) {
	if !validLanguage(lang) {
		lang = DefaultLanguage
	}
	values := make(map[string]string, len(t.Params))
	empty := make(map[string]bool)
	for _, p := range t.Params {
		v, ok := formatValue(p, params[p.Name], lang)
		if !ok {
			if !p.Optional {
				return Message{}, fmt.Errorf("template %s: missing param %s", t.Key, p.Name)
			}
			empty[p.Name] = true
		}
		values[p.Name] = v
	}

	var lines []string
	for _, line := range strings.Split(body, "\n") {
		names, err := Placeholders(line)
		if err != nil {
			return Message{}, fmt.Errorf("template %s: %w", t.Key, err)
		}
		drop := false
		for _, n := range names {
			if _, known := values[n]; !known {
				return Message{}, fmt.Errorf("template %s: unknown placeholder {{%s}}", t.Key, n)
			}
			if empty[n] {
				drop = true
			}
		}
		if !drop {
			lines = append(lines, placeholderRe.ReplaceAllStringFunc(line, func(m string) string {
				return values[m[2:len(m)-2]]
			}))
		}
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return Message{}, fmt.Errorf("template %s: rendered text is empty", t.Key)
	}

	msg := Message{Text: text}
	if t.Button != nil && miniAppURL != "" {
		if link, ok := buttonURL(miniAppURL, t.Button.StartParam, params); ok {
			label := t.Button.Label[lang]
			if label == "" {
				label = t.Button.Label[DefaultLanguage]
			}
			msg.Markup = &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
				{Text: label, WebApp: &telegram.WebAppInfo{URL: link}},
			}}}
		}
	}
	return msg, nil
}

// buttonURL builds <miniAppURL>?startapp=<param> (ARCHITECTURE §18). Telegram
// accepts only HTTPS web_app URLs, so a plain-HTTP development URL yields no
// button rather than a message the Bot API would reject.
func buttonURL(miniAppURL, pattern string, params map[string]any) (string, bool) {
	sp := placeholderRe.ReplaceAllStringFunc(pattern, func(m string) string {
		s, _ := toString(params[m[2:len(m)-2]])
		return s
	})
	if !startParamRe.MatchString(sp) {
		return "", false
	}
	u, err := url.Parse(miniAppURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	q := u.Query()
	q.Set("startapp", sp)
	u.RawQuery = q.Encode()
	return u.String(), true
}

// formatValue renders one param; ok is false when the value is missing/empty.
func formatValue(p Param, v any, lang string) (string, bool) {
	if isEmpty(v) {
		return "", false
	}
	switch p.Kind {
	case KindMoney:
		n, ok := toInt64(v)
		if !ok {
			return "", false
		}
		return FormatMoney(n), true
	case KindNumber:
		n, ok := toInt64(v)
		if !ok {
			return "", false
		}
		return strconv.FormatInt(n, 10), true
	case KindCategory:
		s, _ := toString(v)
		if l, ok := categoryLabels[s][lang]; ok {
			return l, true
		}
		return html.EscapeString(s), true
	default:
		s, ok := toString(v)
		if !ok {
			return "", false
		}
		s = cleanParam(s)
		if s == "" {
			return "", false
		}
		return html.EscapeString(s), true
	}
}

// FormatMoney renders integer KZT as "1 500 ₸" with no-break spaces, so the
// amount never wraps across lines.
func FormatMoney(n int64) string {
	sign := ""
	u := uint64(n)
	if n < 0 {
		sign = "-"
		u = uint64(-(n + 1)) + 1
	}
	digits := strconv.FormatUint(u, 10)
	var b strings.Builder
	b.WriteString(sign)
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(nbsp)
		}
		b.WriteRune(r)
	}
	b.WriteString(nbsp + tenge)
	return b.String()
}

// cleanParam collapses whitespace (so a value cannot break the line structure),
// strips control and bidi-override characters and cuts to maxTextParam.
func cleanParam(s string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case unicode.IsControl(r), r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0xFEFF:
			continue
		}
		if n >= maxTextParam {
			b.WriteString("…")
			break
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case *string:
		return x == nil || strings.TrimSpace(*x) == ""
	}
	return false
}

func toString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case *string:
		if x == nil {
			return "", false
		}
		return *x, true
	case json.Number:
		return x.String(), true
	case fmt.Stringer:
		return x.String(), true
	case int, int32, int64, uint, uint32, uint64:
		return fmt.Sprint(x), true
	case float64:
		if n, ok := toInt64(x); ok {
			return strconv.FormatInt(n, 10), true
		}
		return strconv.FormatFloat(x, 'f', -1, 64), true
	}
	return "", false
}

func toInt64(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint32:
		return int64(x), true
	case uint:
		if uint64(x) > math.MaxInt64 {
			return 0, false
		}
		return int64(x), true
	case uint64:
		if x > math.MaxInt64 {
			return 0, false
		}
		return int64(x), true
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
			return 0, false
		}
		return int64(x), true
	case json.Number:
		n, err := x.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n, err == nil
	}
	return 0, false
}

var allowedTags = map[string]bool{
	"b": true, "strong": true, "i": true, "em": true, "u": true, "ins": true, "s": true, "strike": true,
	"del": true, "code": true, "pre": true, "blockquote": true, "tg-spoiler": true, "a": true,
}

// ValidateHTML checks that s is well-formed for Telegram's HTML parse mode:
// only supported tags (attributes only href on <a>), properly nested, and every
// '<', '>' and '&' part of a tag or a supported entity.
func ValidateHTML(s string) error {
	var stack []string
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return errors.New("unclosed '<': escape it as &lt;")
			}
			tag := s[i+1 : i+end]
			if name, ok := strings.CutPrefix(tag, "/"); ok {
				if len(stack) == 0 || stack[len(stack)-1] != name {
					return fmt.Errorf("unexpected closing tag </%s>", name)
				}
				stack = stack[:len(stack)-1]
			} else {
				name, attrs, _ := strings.Cut(tag, " ")
				if !allowedTags[name] {
					return fmt.Errorf("tag <%s> is not supported", name)
				}
				if attrs = strings.TrimSpace(attrs); attrs != "" && (name != "a" || !hrefAttrRe.MatchString(attrs)) {
					return fmt.Errorf("unsupported attributes on <%s>", name)
				}
				stack = append(stack, name)
			}
			i += end + 1
		case '>':
			return errors.New("unescaped '>': use &gt;")
		case '&':
			m := entityRe.FindString(s[i:])
			if m == "" {
				return errors.New("unescaped '&': use &amp;")
			}
			i += len(m)
		default:
			i++
		}
	}
	if len(stack) > 0 {
		return fmt.Errorf("unclosed tag <%s>", stack[len(stack)-1])
	}
	return nil
}

// overrideKey identifies an admin override (notification_templates row).
type overrideKey struct{ key, lang string }

// loadOverrides reads admin overrides for the given template keys.
func loadOverrides(ctx context.Context, q db.Querier, keys []string) (map[overrideKey]string, error) {
	rows, err := q.Query(ctx, `SELECT key, language, body FROM notification_templates WHERE key = ANY($1)`, keys)
	if err != nil {
		return nil, fmt.Errorf("notifications: load overrides: %w", err)
	}
	defer rows.Close()
	out := make(map[overrideKey]string)
	for rows.Next() {
		var k overrideKey
		var body string
		if err := rows.Scan(&k.key, &k.lang, &body); err != nil {
			return nil, fmt.Errorf("notifications: load overrides: %w", err)
		}
		out[k] = body
	}
	return out, rows.Err()
}

// RenderFor renders a template for one recipient, preferring a valid admin
// override for lang over the default body. It is used by the worker and by
// admin previews.
func RenderFor(ctx context.Context, q db.Querier, key, lang string, params map[string]any, miniAppURL string) (Message, error) {
	t, ok := Lookup(key)
	if !ok {
		return Message{}, fmt.Errorf("%w: %q", ErrUnknownTemplate, key)
	}
	if !validLanguage(lang) {
		lang = DefaultLanguage
	}
	overrides, err := loadOverrides(ctx, q, []string{key})
	if err != nil {
		return Message{}, err
	}
	body, _ := pickBody(t, lang, overrides)
	return t.Render(lang, body, params, miniAppURL)
}

// pickBody returns the override for (template, lang) when it is still valid for
// the template's params, else the default; overrideInvalid reports a skipped
// override so the caller can log it.
func pickBody(t *Template, lang string, overrides map[overrideKey]string) (body string, overrideInvalid bool) {
	if b, ok := overrides[overrideKey{t.Key, lang}]; ok {
		if t.ValidateBody(b) == nil {
			return b, false
		}
		return t.DefaultBody(lang), true
	}
	return t.DefaultBody(lang), false
}
