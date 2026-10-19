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
	RecipientBIN  string    `json:"recipient_bin,omitempty"` // company.bin, shown to drivers as the payee
	Instructions  Localized `json:"instructions"`
}

// KaspiPayment returns the effective Kaspi settings.
func KaspiPayment(ctx context.Context, q db.Querier) Kaspi {
	var k Kaspi
	_ = Effective(ctx, q, "kaspi.payment_url", &k.PaymentURL)
	_ = Effective(ctx, q, "kaspi.recipient_name", &k.RecipientName)
	_ = Effective(ctx, q, "company.bin", &k.RecipientBIN)
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

// Commission modes (setting commission.mode, orders.commission_mode).
const (
	CommissionFixed   = "fixed"
	CommissionPercent = "percent"
)

// CommissionPolicy is how the commission of a new order is computed
// (ARCHITECTURE §22.2): a fixed amount per order, or the tariff's
// commission_bps of the fare. It is charged when a driver takes the order.
type CommissionPolicy struct {
	Mode        string `json:"mode"`
	FixedAmount int64  `json:"fixed_amount"`
}

// CommissionBounds returns the range an admin may set the fixed commission
// in (commission.fixed_min..commission.fixed_max, §22.7: 50..100 KZT).
func CommissionBounds(ctx context.Context, q db.Querier) (lo, hi int64) {
	lo, hi = effectiveInt(ctx, q, "commission.fixed_min"), effectiveInt(ctx, q, "commission.fixed_max")
	if hi < lo {
		hi = lo
	}
	return lo, hi
}

// Commission returns the effective commission policy. The fixed amount is
// kept within CommissionBounds even when a stored value is not.
func Commission(ctx context.Context, q db.Querier) CommissionPolicy {
	lo, hi := CommissionBounds(ctx, q)
	p := CommissionPolicy{Mode: CommissionFixed, FixedAmount: min(max(effectiveInt(ctx, q, "commission.fixed_amount"), lo), hi)}
	var mode string
	if Effective(ctx, q, "commission.mode", &mode) == nil && mode == CommissionPercent {
		p.Mode = CommissionPercent
	}
	return p
}

// WelcomeBonus returns the bonus credited to a new driver on first approval
// (0 disables it).
func WelcomeBonus(ctx context.Context, q db.Querier) int64 {
	return max(effectiveInt(ctx, q, "driver.welcome_bonus"), 0)
}

// PassengerCancelsAfterAcceptPerDay returns how many orders a passenger may
// cancel after a driver accepted them (the driver keeps paying the
// commission, §22.2) within 24 hours before new orders are refused; 0 means
// no limit.
func PassengerCancelsAfterAcceptPerDay(ctx context.Context, q db.Querier) int64 {
	return max(effectiveInt(ctx, q, "orders.passenger_cancels_after_accept_per_day"), 0)
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

// DispatchSettings is the radius-based dispatch policy (ARCHITECTURE §7): a
// searching order is offered to free drivers whose fresh location lies within
// the order's effective radius of the pickup point. The radius starts at
// RadiusBaseM and grows by RadiusStepM every RadiusStepS seconds of search, up
// to RadiusMaxM. A location older than LocationMaxAgeS seconds does not count.
type DispatchSettings struct {
	RadiusBaseM     int `json:"radius_base_m"`
	RadiusStepM     int `json:"radius_step_m"`
	RadiusStepS     int `json:"radius_step_s"`
	RadiusMaxM      int `json:"radius_max_m"`
	LocationMaxAgeS int `json:"location_max_age_s"`
}

// Dispatch returns the effective dispatch settings. The maximum radius is
// never below the base radius, whatever is stored.
func Dispatch(ctx context.Context, q db.Querier) DispatchSettings {
	d := DispatchSettings{
		RadiusBaseM:     int(effectiveInt(ctx, q, "dispatch.radius_base_m")),
		RadiusStepM:     int(effectiveInt(ctx, q, "dispatch.radius_step_m")),
		RadiusStepS:     int(effectiveInt(ctx, q, "dispatch.radius_step_s")),
		RadiusMaxM:      int(effectiveInt(ctx, q, "dispatch.radius_max_m")),
		LocationMaxAgeS: int(effectiveInt(ctx, q, "dispatch.location_max_age_s")),
	}
	d.RadiusMaxM = max(d.RadiusMaxM, d.RadiusBaseM)
	d.RadiusStepS = max(d.RadiusStepS, 1)
	return d
}

// RadiusAt returns the effective dispatch radius in metres for an order that
// has been searching for age: min(max, base + step * floor(age / step_s)).
func (d DispatchSettings) RadiusAt(age time.Duration) int {
	if age < 0 {
		age = 0
	}
	steps := int64(age / (time.Duration(max(d.RadiusStepS, 1)) * time.Second))
	r := int64(d.RadiusBaseM) + int64(d.RadiusStepM)*steps
	return int(min(r, int64(max(d.RadiusMaxM, d.RadiusBaseM))))
}

// LocationMaxAge is how long a driver's location fix stays fresh.
func (d DispatchSettings) LocationMaxAge() time.Duration {
	return time.Duration(d.LocationMaxAgeS) * time.Second
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

// AutoConfirm is the policy for crediting top-ups automatically from Kaspi
// PDF receipts (ARCHITECTURE §6.1). Limits count per driver and business day.
type AutoConfirm struct {
	Enabled         bool          `json:"enabled"`
	MaxPerDay       int64         `json:"max_per_day"`
	MaxAmountPerDay int64         `json:"max_amount_per_day"`
	ReceiptMaxAge   time.Duration `json:"-"`
}

// ReceiptAutoConfirm returns the effective automatic crediting policy.
func ReceiptAutoConfirm(ctx context.Context, q db.Querier) AutoConfirm {
	var a AutoConfirm
	_ = Effective(ctx, q, "topup.auto_confirm_enabled", &a.Enabled)
	a.MaxPerDay = effectiveInt(ctx, q, "topup.auto_max_per_day")
	a.MaxAmountPerDay = effectiveInt(ctx, q, "topup.auto_max_amount_per_day")
	a.ReceiptMaxAge = time.Duration(effectiveInt(ctx, q, "topup.receipt_max_age_hours")) * time.Hour
	return a
}

// Company is the payee whose Kaspi receipts are accepted: the Kaspi
// recipient name and the company requisites.
type Company struct {
	Name    string `json:"name"`
	BIN     string `json:"bin"`
	IBAN    string `json:"iban"`
	Bank    string `json:"bank"`
	BIC     string `json:"bic"`
	KBe     string `json:"kbe"`
	Address string `json:"address"`
}

// CompanyDetails returns the effective payee details.
func CompanyDetails(ctx context.Context, q db.Querier) Company {
	var c Company
	_ = Effective(ctx, q, "kaspi.recipient_name", &c.Name)
	_ = Effective(ctx, q, "company.bin", &c.BIN)
	_ = Effective(ctx, q, "company.iban", &c.IBAN)
	_ = Effective(ctx, q, "company.bank", &c.Bank)
	_ = Effective(ctx, q, "company.bic", &c.BIC)
	_ = Effective(ctx, q, "company.kbe", &c.KBe)
	_ = Effective(ctx, q, "company.address", &c.Address)
	return c
}
