package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// Env holds environment-provided defaults for the keys that have one. The app
// sets it once at startup with Configure; a database row always wins over it.
type Env struct {
	KaspiPaymentURL    string
	KaspiRecipientName string
	// KaspiInstructions is either plain text used for every language or a JSON
	// object {"kk":…,"ru":…,"en":…}.
	KaspiInstructions        string
	TwoGISBaseURL            string
	DriverOnlineIdleTimeout  time.Duration
	MaxRoutePoints           int
	DefaultCommissionPercent int
}

var envPtr atomic.Pointer[Env]

// Configure installs the environment defaults.
func Configure(e Env) { envPtr.Store(&e) }

// CurrentEnv returns the installed environment defaults.
func CurrentEnv() Env {
	if p := envPtr.Load(); p != nil {
		return *p
	}
	return Env{}
}

func envInstructions(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if strings.HasPrefix(s, "{") {
		var l Localized
		if err := json.Unmarshal([]byte(s), &l); err == nil && l.KK != "" && l.RU != "" && l.EN != "" {
			return l, true
		}
	}
	return Localized{KK: s, RU: s, EN: s}, true
}

// Effective decodes a registered key into dst, resolving database value → env
// default → built-in default. A stored value that no longer passes validation
// is ignored (and logged) rather than served.
func Effective(ctx context.Context, q db.Querier, key string, dst any) error {
	def, ok := Lookup(key)
	if !ok {
		return fmt.Errorf("settings: unknown key %q", key)
	}
	raw, found, err := load(ctx, q, key)
	switch {
	case err != nil:
		httpx.Logger(ctx).Warn("setting unreadable, using default", slog.String("key", key), slog.Any("err", err))
	case found:
		norm, reason := def.Normalize(raw)
		if reason == "" {
			if err := json.Unmarshal(norm, dst); err == nil {
				return nil
			}
		}
		httpx.Logger(ctx).Warn("stored setting invalid, using default", slog.String("key", key), slog.String("reason", reason))
	}
	b, err := json.Marshal(def.DefaultValue(CurrentEnv()))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func effectiveInt(ctx context.Context, q db.Querier, key string) int64 {
	var n int64
	_ = Effective(ctx, q, key, &n)
	return n
}

// Kaspi is the public Kaspi Pay payment information shown to drivers.
type Kaspi struct {
	PaymentURL    string    `json:"payment_url"`
	RecipientName string    `json:"recipient_name"`
	Instructions  Localized `json:"instructions"`
}

// KaspiPayment returns the effective Kaspi settings.
func KaspiPayment(ctx context.Context, q db.Querier) Kaspi {
	var k Kaspi
	_ = Effective(ctx, q, "kaspi.payment_url", &k.PaymentURL)
	_ = Effective(ctx, q, "kaspi.recipient_name", &k.RecipientName)
	_ = Effective(ctx, q, "kaspi.instructions", &k.Instructions)
	return k
}

// TopUpLimits are the driver top-up amount rules.
type TopUpLimits struct {
	Min     int64   `json:"min"`
	Max     int64   `json:"max"`
	Presets []int64 `json:"presets"`
}

// TopUp returns the effective top-up limits; presets outside [Min, Max] are dropped.
func TopUp(ctx context.Context, q db.Querier) TopUpLimits {
	t := TopUpLimits{Min: effectiveInt(ctx, q, "topup.min_amount"), Max: effectiveInt(ctx, q, "topup.max_amount")}
	if t.Max < t.Min {
		t.Max = t.Min
	}
	var presets []int64
	_ = Effective(ctx, q, "topup.presets", &presets)
	t.Presets = make([]int64, 0, len(presets))
	for _, p := range presets {
		if p >= t.Min && p <= t.Max {
			t.Presets = append(t.Presets, p)
		}
	}
	return t
}

// TopUpExpiry is how long a created top-up request stays open.
func TopUpExpiry(ctx context.Context, q db.Querier) time.Duration {
	return time.Duration(effectiveInt(ctx, q, "topup.expiry_hours")) * time.Hour
}

// Support holds the public support contacts.
type Support struct {
	TelegramUsername string    `json:"telegram_username"`
	Phone            string    `json:"phone"`
	Hours            Localized `json:"hours"`
}

// SupportContacts returns the effective support contacts.
func SupportContacts(ctx context.Context, q db.Querier) Support {
	var s Support
	_ = Effective(ctx, q, "support.telegram_username", &s.TelegramUsername)
	_ = Effective(ctx, q, "support.phone", &s.Phone)
	_ = Effective(ctx, q, "support.hours", &s.Hours)
	return s
}

// MaxRoutePoints returns the maximum number of route points (2..5).
func MaxRoutePoints(ctx context.Context, q db.Querier) int {
	return int(max(2, min(effectiveInt(ctx, q, "routing.max_points"), 5)))
}

// DefaultCommissionBps returns the default commission in basis points.
func DefaultCommissionBps(ctx context.Context, q db.Querier) int64 {
	return effectiveInt(ctx, q, "commission.default_bps")
}

// MinOnlineBalance returns the minimum available balance required to go online.
func MinOnlineBalance(ctx context.Context, q db.Querier) int64 {
	return effectiveInt(ctx, q, "driver.min_online_balance")
}

// RequirePhoneVerification reports whether driver registration needs a verified phone.
func RequirePhoneVerification(ctx context.Context, q db.Querier) bool {
	var b bool
	_ = Effective(ctx, q, "driver.require_phone_verification", &b)
	return b
}

// DriverOnlineIdleTimeout is how long an online driver may stay silent before
// being set offline.
func DriverOnlineIdleTimeout(ctx context.Context, q db.Querier) time.Duration {
	return time.Duration(effectiveInt(ctx, q, "driver.online_idle_hours")) * time.Hour
}

// ProhibitedItems returns the delivery prohibited-item codes.
func ProhibitedItems(ctx context.Context, q db.Querier) []string {
	var v []string
	_ = Effective(ctx, q, "delivery.prohibited_items", &v)
	return v
}

// BlockedKeywords returns the lower-case words rejected in cargo descriptions.
func BlockedKeywords(ctx context.Context, q db.Querier) []string {
	var v []string
	_ = Effective(ctx, q, "delivery.blocked_keywords", &v)
	return v
}

// TwoGISBaseURL returns the base URL for 2GIS navigation links, without a trailing slash.
func TwoGISBaseURL(ctx context.Context, q db.Querier) string {
	var s string
	_ = Effective(ctx, q, "navigation.twogis_base_url", &s)
	return strings.TrimRight(s, "/")
}
