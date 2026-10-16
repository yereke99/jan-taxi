package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

const (
	appTokenVersion  = 1
	maxAppTokenBytes = 1024
)

// ErrTokenExpired is returned for a well-formed, correctly signed but expired app token.
var ErrTokenExpired = httpx.NewError(http.StatusUnauthorized, "token_expired", "app token expired")

var b64 = base64.RawURLEncoding

// appClaims is the app token payload: {"v":1,"uid":"<uuid>","app":"passenger|driver","exp":<unix>}.
type appClaims struct {
	V   int    `json:"v"`
	UID string `json:"uid"`
	App App    `json:"app"`
	Exp int64  `json:"exp"`
}

// IssueAppToken signs an app token for userID valid for AppTokenTTL.
func (s *Service) IssueAppToken(userID uuid.UUID, app App, now time.Time) (string, time.Time, error) {
	if !app.Valid() || userID == uuid.Nil {
		return "", time.Time{}, httpx.Internal(nil)
	}
	exp := now.Add(s.cfg.AppTokenTTL).Truncate(time.Second).UTC()
	payload, err := json.Marshal(appClaims{V: appTokenVersion, UID: userID.String(), App: app, Exp: exp.Unix()})
	if err != nil {
		return "", time.Time{}, httpx.Internal(err)
	}
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(s.sign(payload)), exp, nil
}

// VerifyAppToken checks the token's signature, version and expiry. It does not
// touch the database, so the returned Principal has TelegramID == 0 and the
// user's blocked flag is not checked; use AuthenticateAppToken for both.
func (s *Service) VerifyAppToken(token string) (Principal, error) {
	p, _, err := s.verifyAppToken(token, s.now())
	return p, err
}

func (s *Service) verifyAppToken(token string, now time.Time) (Principal, time.Time, error) {
	if token == "" || len(token) > maxAppTokenBytes {
		return Principal{}, time.Time{}, httpx.Unauthorized()
	}
	encPayload, encSig, ok := strings.Cut(token, ".")
	if !ok || strings.Contains(encSig, ".") {
		return Principal{}, time.Time{}, httpx.Unauthorized()
	}
	payload, err := b64.DecodeString(encPayload)
	if err != nil {
		return Principal{}, time.Time{}, httpx.Unauthorized()
	}
	sig, err := b64.DecodeString(encSig)
	if err != nil || !hmac.Equal(sig, s.sign(payload)) {
		return Principal{}, time.Time{}, httpx.Unauthorized()
	}
	var c appClaims
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil || c.V != appTokenVersion || !c.App.Valid() {
		return Principal{}, time.Time{}, httpx.Unauthorized()
	}
	uid, err := uuid.Parse(c.UID)
	if err != nil || uid == uuid.Nil {
		return Principal{}, time.Time{}, httpx.Unauthorized()
	}
	exp := time.Unix(c.Exp, 0).UTC()
	if !now.Before(exp) {
		return Principal{}, time.Time{}, ErrTokenExpired
	}
	return Principal{UserID: uid, App: c.App}, exp, nil
}

func (s *Service) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}
