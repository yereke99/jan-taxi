package settings

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		key    string
		in     string
		want   string
		reason string
	}{
		{"routing.max_points", `4`, `4`, ""},
		{"routing.max_points", `6`, ``, ReasonOutOfRange},
		{"routing.max_points", `1`, ``, ReasonOutOfRange},
		{"routing.max_points", `4.5`, ``, ReasonInvalidType},
		{"routing.max_points", `"4"`, ``, ReasonInvalidType},
		{"routing.max_points", `4 5`, ``, ReasonInvalidType},
		{"commission.default_bps", `0`, `0`, ""},
		{"commission.default_bps", `5001`, ``, ReasonOutOfRange},
		{"driver.require_phone_verification", `false`, `false`, ""},
		{"driver.require_phone_verification", `"false"`, ``, ReasonInvalidType},
		{"support.telegram_username", `"@JanTaxi_support"`, `"JanTaxi_support"`, ""},
		{"support.telegram_username", `"https://t.me/jantaxi_help"`, `"jantaxi_help"`, ""},
		{"support.telegram_username", `""`, `""`, ""},
		{"support.telegram_username", `"bad name"`, ``, ReasonInvalid},
		{"support.phone", `"8 (701) 234-56-78"`, `"+77012345678"`, ""},
		{"support.phone", `"+7 7132 55 66 77"`, `"+77132556677"`, ""},
		{"support.phone", `"+1 555 0100"`, ``, ReasonInvalid},
		{"support.hours", `{"kk":" Күн сайын  09:00–21:00 ","ru":"Ежедневно 09:00–21:00","en":"Daily 09:00–21:00"}`,
			`{"kk":"Күн сайын 09:00–21:00","ru":"Ежедневно 09:00–21:00","en":"Daily 09:00–21:00"}`, ""},
		{"support.hours", `{"kk":"","ru":"","en":""}`, `{"kk":"","ru":"","en":""}`, ""},
		{"support.hours", `{"kk":"Күн сайын","ru":"","en":""}`, ``, ReasonIncomplete},
		{"support.hours", `{"kk":"a","ru":"b","en":"c","de":"d"}`, ``, ReasonInvalid},
		{"support.hours", `"Daily"`, ``, ReasonInvalidType},
		{"topup.presets", `[5000, 1000, 2000]`, `[1000,2000,5000]`, ""},
		{"topup.presets", `[]`, `[]`, ""},
		{"topup.presets", `[1000, 1000]`, ``, ReasonDuplicate},
		{"topup.presets", `[1,2,3,4,5,6,7,8,9]`, ``, ReasonTooMany},
		{"topup.presets", `[0]`, ``, ReasonOutOfRange},
		{"delivery.prohibited_items", `["weapons","drugs"]`, `["weapons","drugs"]`, ""},
		{"delivery.prohibited_items", `["Weapons"]`, ``, ReasonInvalid},
		{"delivery.blocked_keywords", `["ҚАРУ","Оружие"]`, `["қару","оружие"]`, ""},
		{"delivery.blocked_keywords", `["x"]`, ``, ReasonTooShort},
		{"delivery.blocked_keywords", `["Drug","drug"]`, ``, ReasonDuplicate},
		{"kaspi.payment_url", `"https://pay.kaspi.kz/pay/abc123"`, `"https://pay.kaspi.kz/pay/abc123"`, ""},
		{"kaspi.payment_url", `""`, `""`, ""},
		{"kaspi.payment_url", `"http://pay.kaspi.kz/pay/abc"`, ``, ReasonInvalid},
		{"kaspi.payment_url", `"https://kaspi.kz.evil.example/pay"`, ``, ReasonInvalid},
		{"kaspi.payment_url", `"https://user@pay.kaspi.kz/x"`, ``, ReasonInvalid},
		{"kaspi.recipient_name", `"  ИП Jan  Taxi "`, `"ИП Jan Taxi"`, ""},
		{"navigation.twogis_base_url", `"https://2gis.kz/"`, `"https://2gis.kz"`, ""},
		{"navigation.twogis_base_url", `"https://2gis.kz/almaty"`, ``, ReasonInvalid},
		{"navigation.twogis_base_url", `""`, ``, ReasonRequired},
		{"navigation.twogis_base_url", `"https://maps.example.com"`, ``, ReasonInvalid},
		{"driver.online_idle_hours", `24`, `24`, ""},
		{"driver.online_idle_hours", `0`, ``, ReasonOutOfRange},
		{"topup.expiry_hours", `null`, ``, ReasonInvalidType},
		{"dispatch.radius_base_m", `5000`, `5000`, ""},
		{"dispatch.radius_base_m", `499`, ``, ReasonOutOfRange},
		{"dispatch.radius_base_m", `50001`, ``, ReasonOutOfRange},
		{"dispatch.radius_step_m", `0`, `0`, ""},
		{"dispatch.radius_step_m", `20001`, ``, ReasonOutOfRange},
		{"dispatch.radius_step_s", `9`, ``, ReasonOutOfRange},
		{"dispatch.radius_step_s", `600`, `600`, ""},
		{"dispatch.radius_max_m", `100000`, `100000`, ""},
		{"dispatch.radius_max_m", `100001`, ``, ReasonOutOfRange},
		{"dispatch.location_max_age_s", `29`, ``, ReasonOutOfRange},
		{"dispatch.location_max_age_s", `3600`, `3600`, ""},
		{"dispatch.location_max_age_s", `"300"`, ``, ReasonInvalidType},
		{"kaspi.payment_url", `"https://pay.kaspi.kz/pay/xbp1imhd"`, `"https://pay.kaspi.kz/pay/xbp1imhd"`, ""},
		{"kaspi.recipient_name", `"ИП \"JAN-TAXI\""`, `"ИП \"JAN-TAXI\""`, ""},
		{"company.bin", `"000809500289"`, `"000809500289"`, ""},
		{"company.bin", `"0008 0950 0289"`, `"000809500289"`, ""},
		{"company.bin", `"000809500288"`, ``, ReasonInvalid}, // wrong check digit
		{"company.bin", `"12345"`, ``, ReasonInvalid},
		{"company.iban", `"kz05 722S 0000 5717 6693"`, `"KZ05722S000057176693"`, ""},
		{"company.iban", `"KZ06722S000057176693"`, ``, ReasonInvalid}, // wrong check digits
		{"company.iban", `"DE89370400440532013000"`, ``, ReasonInvalid},
		{"company.bic", `"caspkzka"`, `"CASPKZKA"`, ""},
		{"company.bic", `"CASP"`, ``, ReasonInvalid},
		{"company.kbe", `"19"`, `"19"`, ""},
		{"company.kbe", `"1"`, ``, ReasonInvalid},
		{"company.bank", `" АО  \"Kaspi Bank\" "`, `"АО \"Kaspi Bank\""`, ""},
		{"topup.auto_confirm_enabled", `false`, `false`, ""},
		{"topup.auto_max_per_day", `0`, `0`, ""},
		{"topup.auto_max_per_day", `101`, ``, ReasonOutOfRange},
		{"topup.auto_max_amount_per_day", `50000`, `50000`, ""},
		{"topup.auto_max_amount_per_day", `-1`, ``, ReasonOutOfRange},
		{"topup.receipt_max_age_hours", `0`, ``, ReasonOutOfRange},
		{"topup.receipt_max_age_hours", `72`, `72`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.key+" "+tt.in, func(t *testing.T) {
			d, ok := Lookup(tt.key)
			if !ok {
				t.Fatalf("key %s not registered", tt.key)
			}
			got, reason := d.Normalize(json.RawMessage(tt.in))
			if reason != tt.reason {
				t.Fatalf("reason = %q, want %q", reason, tt.reason)
			}
			if tt.reason == "" && string(got) != tt.want {
				t.Fatalf("value = %s, want %s", got, tt.want)
			}
		})
	}
}

// Every built-in default must pass its own validation, otherwise Effective
// would serve values an admin could never set.
func TestDefaultsAreValid(t *testing.T) {
	for _, d := range Defs() {
		b, err := json.Marshal(d.Default)
		if err != nil {
			t.Fatalf("%s: %v", d.Key, err)
		}
		if _, reason := d.Normalize(b); reason != "" {
			t.Errorf("%s default %s invalid: %s", d.Key, b, reason)
		}
	}
}

func TestRegistryCoversSeedAndExtraKeys(t *testing.T) {
	want := []string{
		"support.telegram_username", "support.phone", "support.hours", "routing.max_points",
		"commission.default_bps", "driver.min_online_balance", "driver.require_phone_verification",
		"topup.min_amount", "topup.max_amount", "topup.expiry_hours", "topup.presets",
		"delivery.prohibited_items", "delivery.blocked_keywords",
		"kaspi.payment_url", "kaspi.recipient_name", "kaspi.instructions",
		"navigation.twogis_base_url", "driver.online_idle_hours",
		"dispatch.radius_base_m", "dispatch.radius_step_m", "dispatch.radius_step_s", "dispatch.radius_max_m",
		"dispatch.location_max_age_s",
		"topup.auto_confirm_enabled", "topup.auto_max_per_day", "topup.auto_max_amount_per_day", "topup.receipt_max_age_hours",
		"company.bin", "company.iban", "company.bank", "company.bic", "company.kbe", "company.address",
		"commission.mode", "commission.fixed_amount", "commission.fixed_min", "commission.fixed_max", "driver.welcome_bonus",
		"orders.passenger_cancels_after_accept_per_day",
	}
	for _, k := range want {
		if _, ok := Lookup(k); !ok {
			t.Errorf("missing key %s", k)
		}
	}
	if len(Defs()) != len(want) {
		t.Errorf("registry has %d keys, want %d", len(Defs()), len(want))
	}
}

func TestDefaultValueUsesEnv(t *testing.T) {
	env := Env{
		KaspiPaymentURL: "https://pay.kaspi.kz/pay/x", KaspiInstructions: "Pay now",
		TwoGISBaseURL: "https://2gis.kz/", DriverOnlineIdleTimeout: 90 * time.Minute,
		MaxRoutePoints: 4, DefaultCommissionPercent: 12,
	}
	tests := []struct {
		key  string
		want string
	}{
		{"kaspi.payment_url", `"https://pay.kaspi.kz/pay/x"`},
		{"kaspi.recipient_name", `""`}, // env empty → built-in default
		{"kaspi.instructions", `{"kk":"Pay now","ru":"Pay now","en":"Pay now"}`},
		{"navigation.twogis_base_url", `"https://2gis.kz"`},
		{"driver.online_idle_hours", `2`},
		{"routing.max_points", `4`},
		{"commission.default_bps", `1200`},
		{"topup.min_amount", `500`},
	}
	for _, tt := range tests {
		d, _ := Lookup(tt.key)
		b, _ := json.Marshal(d.DefaultValue(env))
		if string(b) != tt.want {
			t.Errorf("%s = %s, want %s", tt.key, b, tt.want)
		}
	}
	d, _ := Lookup("kaspi.instructions")
	b, _ := json.Marshal(d.DefaultValue(Env{KaspiInstructions: `{"kk":"Төлеңіз","ru":"Оплатите","en":"Pay"}`}))
	if string(b) != `{"kk":"Төлеңіз","ru":"Оплатите","en":"Pay"}` {
		t.Errorf("JSON instructions env = %s", b)
	}
}

func TestLocalizedGet(t *testing.T) {
	l := Localized{KK: "a", RU: "b", EN: "c"}
	for lang, want := range map[string]string{"kk": "a", "ru": "b", "en": "c", "de": "a", "": "a"} {
		if got := l.Get(lang); got != want {
			t.Errorf("Get(%q) = %q, want %q", lang, got, want)
		}
	}
}

func TestDispatchRadiusAt(t *testing.T) {
	d := DispatchSettings{RadiusBaseM: 5000, RadiusStepM: 2500, RadiusStepS: 60, RadiusMaxM: 10000, LocationMaxAgeS: 300}
	tests := []struct {
		age  time.Duration
		want int
	}{
		{-time.Second, 5000}, {0, 5000}, {59 * time.Second, 5000}, {60 * time.Second, 7500},
		{119 * time.Second, 7500}, {120 * time.Second, 10000}, {time.Hour, 10000}, {1000 * time.Hour, 10000},
	}
	for _, tt := range tests {
		if got := d.RadiusAt(tt.age); got != tt.want {
			t.Errorf("RadiusAt(%v) = %d, want %d", tt.age, got, tt.want)
		}
	}
	if got := (DispatchSettings{RadiusBaseM: 3000, RadiusStepM: 0, RadiusStepS: 60, RadiusMaxM: 15000}).RadiusAt(time.Hour); got != 3000 {
		t.Errorf("no growth = %d", got)
	}
	if d.LocationMaxAge() != 5*time.Minute {
		t.Errorf("LocationMaxAge = %v", d.LocationMaxAge())
	}
}
