// Command devlink prints local development URLs for the Mini Apps with
// correctly signed Telegram initData, so the apps can be opened in a desktop
// browser against a local API without Telegram:
//
//	PASSENGER_BOT_TOKEN=... go run ./admin/backend/cmd/devlink --app passenger --start-param taxi
//
// The URL carries the launch parameters in the fragment exactly as Telegram
// passes them to a Mini App. The tool refuses to run when APP_ENV=production.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, time.Now()))
}

var startParamRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type options struct {
	app, name, username, lang, startParam, base string
	tgID                                        int64
	initDataOnly                                bool
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer, now time.Time) int {
	if strings.EqualFold(strings.TrimSpace(getenv("APP_ENV")), "production") {
		fmt.Fprintln(stderr, "devlink is a development tool and refuses to run with APP_ENV=production")
		return 2
	}
	var o options
	fs := flag.NewFlagSet("devlink", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.app, "app", "passenger", "passenger or driver")
	fs.Int64Var(&o.tgID, "tg-id", 100000001, "Telegram user id to sign in as")
	fs.StringVar(&o.name, "name", "Dev", "Telegram first name")
	fs.StringVar(&o.username, "username", "", "Telegram username (default u<tg-id>)")
	fs.StringVar(&o.lang, "lang", "kk", "Telegram language_code")
	fs.StringVar(&o.startParam, "start-param", "", "start parameter, e.g. taxi, orders, order_<uuid>")
	fs.StringVar(&o.base, "base", "", "Mini App URL (default http://localhost:5173/passenger/ or http://localhost:5174/driver/)")
	fs.BoolVar(&o.initDataOnly, "init-data", false, "print only the signed initData (for curl against /api/v1/auth/telegram/<app>)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	link, initData, err := build(o, getenv, now)
	if err != nil {
		fmt.Fprintln(stderr, "devlink:", err)
		return 2
	}
	if o.initDataOnly {
		fmt.Fprintln(stdout, initData)
		return 0
	}
	fmt.Fprintln(stdout, link)
	fmt.Fprintf(stderr, "Signed for Telegram user %d with the %s bot token; valid while the API accepts auth_date %s.\n",
		o.tgID, o.app, now.UTC().Format(time.RFC3339))
	return 0
}

func build(o options, getenv func(string) string, now time.Time) (link, initData string, err error) {
	var tokenEnv, defaultBase string
	switch o.app {
	case "passenger":
		tokenEnv, defaultBase = "PASSENGER_BOT_TOKEN", "http://localhost:5173/passenger/"
	case "driver":
		tokenEnv, defaultBase = "DRIVER_BOT_TOKEN", "http://localhost:5174/driver/"
	default:
		return "", "", errors.New("--app must be passenger or driver")
	}
	token := strings.TrimSpace(getenv(tokenEnv))
	if token == "" {
		return "", "", fmt.Errorf("%s is not set (use the same value as the local API)", tokenEnv)
	}
	if o.tgID <= 0 {
		return "", "", errors.New("--tg-id must be positive")
	}
	if o.startParam != "" && !startParamRe.MatchString(o.startParam) {
		return "", "", errors.New("--start-param must match ^[A-Za-z0-9_-]{1,64}$")
	}
	base := o.base
	if base == "" {
		base = defaultBase
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", errors.New("--base must be an absolute http(s) URL")
	}
	if !isLocalHost(u.Hostname()) {
		return "", "", errors.New("--base must point to a local development host (localhost, 127.0.0.1, *.localhost, *.test)")
	}
	username := o.username
	if username == "" {
		username = "u" + strconv.FormatInt(o.tgID, 10)
	}

	initData, err = signInitData(token, initUser{
		ID: o.tgID, FirstName: o.name, Username: username, LanguageCode: o.lang, AllowsWriteToPM: true,
	}, now, o.startParam)
	if err != nil {
		return "", "", err
	}
	theme, err := json.Marshal(whiteTheme)
	if err != nil {
		return "", "", err
	}
	frag := "tgWebAppData=" + url.QueryEscape(initData) +
		"&tgWebAppVersion=8.0&tgWebAppPlatform=ios" +
		"&tgWebAppThemeParams=" + url.QueryEscape(string(theme))
	if o.startParam != "" {
		frag += "&tgWebAppStartParam=" + url.QueryEscape(o.startParam)
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String() + "#" + frag, initData, nil
}

func isLocalHost(h string) bool {
	h = strings.ToLower(h)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".test") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

type initUser struct {
	ID              int64  `json:"id"`
	FirstName       string `json:"first_name"`
	LastName        string `json:"last_name,omitempty"`
	Username        string `json:"username,omitempty"`
	LanguageCode    string `json:"language_code,omitempty"`
	AllowsWriteToPM bool   `json:"allows_write_to_pm"`
}

// signInitData produces initData the way Telegram does for a Mini App launch:
// secret = HMAC_SHA256(key "WebAppData", bot token); hash = hex(HMAC_SHA256(
// secret, data-check-string)), where the data-check-string is every field
// except hash, sorted by key, "key=value" joined by "\n". The signature field
// (Telegram's Ed25519 third-party signature) cannot be produced locally; a
// placeholder is included because SDK parsers expect the field, and it is
// covered by the HMAC like every other field.
func signInitData(botToken string, user initUser, authDate time.Time, startParam string) (string, error) {
	userJSON, err := json.Marshal(user)
	if err != nil {
		return "", err
	}
	fields := map[string]string{
		"user":      string(userJSON),
		"auth_date": strconv.FormatInt(authDate.Unix(), 10),
		"query_id":  "AAHdev" + strconv.FormatInt(user.ID, 10),
		"signature": "ZGV2bGluay1sb2NhbC1vbmx5",
	}
	if startParam != "" {
		fields["start_param"] = startParam
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + fields[k]
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))

	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v.Encode(), nil
}

// whiteTheme mirrors Telegram's light iOS theme with the product's
// black-and-white palette (ARCHITECTURE §15).
var whiteTheme = map[string]string{
	"bg_color":                  "#ffffff",
	"secondary_bg_color":        "#f2f2f7",
	"section_bg_color":          "#ffffff",
	"header_bg_color":           "#ffffff",
	"bottom_bar_bg_color":       "#ffffff",
	"text_color":                "#000000",
	"hint_color":                "#8e8e93",
	"subtitle_text_color":       "#8e8e93",
	"section_header_text_color": "#6d6d72",
	"section_separator_color":   "#c6c6c8",
	"link_color":                "#007aff",
	"accent_text_color":         "#007aff",
	"button_color":              "#000000",
	"button_text_color":         "#ffffff",
	"destructive_text_color":    "#ff3b30",
}
