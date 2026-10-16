package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

const (
	maxInitDataBytes  = 4096
	initDataFutureMax = 60 * time.Second
)

// Errors returned by ValidateInitData. They are *httpx.Error values so handlers
// can return them directly; compare with errors.Is.
var (
	ErrInvalidInitData = httpx.NewError(http.StatusUnauthorized, "invalid_init_data", "invalid Telegram init data")
	ErrInitDataExpired = httpx.NewError(http.StatusUnauthorized, "init_data_expired", "Telegram init data expired")
)

// TelegramUser is the "user" object of Mini App initData.
type TelegramUser struct {
	ID              int64  `json:"id"`
	FirstName       string `json:"first_name"`
	LastName        string `json:"last_name"`
	Username        string `json:"username"`
	LanguageCode    string `json:"language_code"`
	AllowsWriteToPM bool   `json:"allows_write_to_pm"`
	PhotoURL        string `json:"photo_url"`
}

// InitData is validated Mini App launch data.
type InitData struct {
	User       TelegramUser
	AuthDate   time.Time
	StartParam string
	QueryID    string
}

// ValidateInitData checks raw initData against the bot token of app, as
// documented at core.telegram.org/bots/webapps: every field except "hash"
// (including "signature") goes into the data-check-string.
func (s *Service) ValidateInitData(raw string, app App, now time.Time) (InitData, error) {
	token := s.botToken(app)
	if token == "" {
		return InitData{}, ErrInvalidInitData
	}
	return validateInitData(raw, token, s.cfg.InitDataMaxAge, now)
}

func validateInitData(raw, botToken string, maxAge time.Duration, now time.Time) (InitData, error) {
	if raw == "" || len(raw) > maxInitDataBytes {
		return InitData{}, ErrInvalidInitData
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return InitData{}, ErrInvalidInitData
	}
	keys := make([]string, 0, len(values))
	for k, v := range values {
		// Telegram never repeats a key; duplicates would make the check ambiguous.
		if len(v) != 1 {
			return InitData{}, ErrInvalidInitData
		}
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	gotHash, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(gotHash) != sha256.Size {
		return InitData{}, ErrInvalidInitData
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(values.Get(k))
	}
	if !hmac.Equal(initDataHash(botToken, b.String()), gotHash) {
		return InitData{}, ErrInvalidInitData
	}

	authUnix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || authUnix <= 0 {
		return InitData{}, ErrInvalidInitData
	}
	authDate := time.Unix(authUnix, 0).UTC()
	if authDate.After(now.Add(initDataFutureMax)) {
		return InitData{}, ErrInvalidInitData
	}
	if now.Sub(authDate) > maxAge {
		return InitData{}, ErrInitDataExpired
	}

	userJSON := values.Get("user")
	if userJSON == "" {
		return InitData{}, ErrInvalidInitData
	}
	var u TelegramUser
	if err := json.Unmarshal([]byte(userJSON), &u); err != nil || u.ID <= 0 {
		return InitData{}, ErrInvalidInitData
	}
	return InitData{
		User:       u,
		AuthDate:   authDate,
		StartParam: values.Get("start_param"),
		QueryID:    values.Get("query_id"),
	}, nil
}

func initDataHash(botToken, dataCheckString string) []byte {
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(dataCheckString))
	return mac.Sum(nil)
}
