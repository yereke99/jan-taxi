package cities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

func TestDirectLink(t *testing.T) {
	tests := []struct {
		bot, short, param, want string
	}{
		{"JanTaxiBot", "", "home", "https://t.me/JanTaxiBot?startapp=home&mode=fullscreen"},
		{"@jantaxi_driver_bot", "", "register", "https://t.me/jantaxi_driver_bot?startapp=register&mode=fullscreen"},
		{"jantaxi_bot", "app", "order_3f1c2a90-1b2c-4d5e-8f90-a1b2c3d4e5f6", "https://t.me/jantaxi_bot/app?startapp=order_3f1c2a90-1b2c-4d5e-8f90-a1b2c3d4e5f6&mode=fullscreen"},
		{"jantaxi_bot", "", "", "https://t.me/jantaxi_bot?startapp&mode=fullscreen"},
		{"jantaxi_bot", "", "bad param!", "https://t.me/jantaxi_bot?startapp&mode=fullscreen"},
		{"jantaxi_bot", "a/b", "home", "https://t.me/jantaxi_bot?startapp=home&mode=fullscreen"},
		{"", "", "home", ""},
		{"x", "", "home", ""},
	}
	for _, tt := range tests {
		if got := DirectLink(tt.bot, tt.short, tt.param); got != tt.want {
			t.Errorf("DirectLink(%q,%q,%q) = %q, want %q", tt.bot, tt.short, tt.param, got, tt.want)
		}
	}
}

var testOptions = PublicOptions{
	PassengerBotUsername: "jantaxi_bot", DriverBotUsername: "jantaxi_driver_bot",
	DriverMiniAppShortName: "drive", MapStyleURL: "https://tiles.openfreemap.org/styles/positron",
	DefaultCity: "Embi", DefaultLanguage: "kk",
}

func getConfig(t *testing.T, s *PublicConfigService, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := chi.NewRouter()
	s.MountPublic(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/public/config"+query, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec, body
}

func TestPublicConfigPassenger(t *testing.T) {
	pool := testutil.NewDB(t)
	settings.Configure(settings.Env{TwoGISBaseURL: "https://2gis.kz"})
	t.Cleanup(func() { settings.Configure(settings.Env{}) })
	if err := settings.Set(context.Background(), pool, "support.telegram_username", "jantaxi_help", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	rec, cfg := getConfig(t, NewPublicConfig(pool, testOptions), "?app=passenger")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control = %q", got)
	}
	if cfg["app"] != "passenger" || cfg["default_language"] != "kk" {
		t.Errorf("app/lang = %v %v", cfg["app"], cfg["default_language"])
	}
	if langs, _ := json.Marshal(cfg["languages"]); string(langs) != `["kk","ru","en"]` {
		t.Errorf("languages = %s", langs)
	}
	city := cfg["city"].(map[string]any)
	if city["code"] != "EMB" || city["zoom"] != float64(14) || city["center"].(map[string]any)["lat"] != 48.8264 ||
		city["name"].(map[string]any)["kk"] != "Ембі" {
		t.Errorf("city = %v", city)
	}
	if cfg["map"].(map[string]any)["style_url"] != testOptions.MapStyleURL || cfg["routing"].(map[string]any)["max_points"] != float64(5) {
		t.Errorf("map/routing = %v %v", cfg["map"], cfg["routing"])
	}
	bots := cfg["bots"].(map[string]any)
	want := map[string]string{
		"passenger_username":   "jantaxi_bot",
		"driver_username":      "jantaxi_driver_bot",
		"passenger_link":       "https://t.me/jantaxi_bot?startapp=home&mode=fullscreen",
		"driver_link":          "https://t.me/jantaxi_driver_bot/drive?startapp=home&mode=fullscreen",
		"driver_register_link": "https://t.me/jantaxi_driver_bot/drive?startapp=register&mode=fullscreen",
	}
	for k, v := range want {
		if bots[k] != v {
			t.Errorf("bots.%s = %v, want %s", k, bots[k], v)
		}
	}
	support := cfg["support"].(map[string]any)
	if support["telegram_username"] != "jantaxi_help" || support["telegram_link"] != "https://t.me/jantaxi_help" ||
		support["phone"] != "" || support["hours"].(map[string]any)["kk"] != "Күн сайын 08:00–23:00" {
		t.Errorf("support = %v", support)
	}
	if items := cfg["delivery"].(map[string]any)["prohibited_items"].([]any); len(items) != 8 {
		t.Errorf("prohibited items = %v", items)
	}
	if cfg["navigation"].(map[string]any)["twogis_base_url"] != "https://2gis.kz" {
		t.Errorf("navigation = %v", cfg["navigation"])
	}
	fare := cfg["fare"].(map[string]any)
	if fare["min"] != float64(500) || fare["max"] != float64(1500) || fare["step"] != float64(50) {
		t.Errorf("fare = %v", fare)
	}
	for _, k := range []string{"kaspi", "topup"} {
		if _, ok := cfg[k]; ok {
			t.Errorf("passenger config leaks %s", k)
		}
	}
}

func TestPublicConfigDriverAndAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	settings.Configure(settings.Env{KaspiPaymentURL: "https://pay.kaspi.kz/pay/env", KaspiRecipientName: "ИП Jan Taxi"})
	t.Cleanup(func() { settings.Configure(settings.Env{}) })
	if err := settings.Set(ctx, pool, "kaspi.recipient_name", "ТОО Jan Taxi", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	s := NewPublicConfig(pool, testOptions)

	_, cfg := getConfig(t, s, "?app=driver")
	kaspi := cfg["kaspi"].(map[string]any)
	instr := kaspi["instructions"].(map[string]any)
	// The payee link seeded by the migrations wins over the environment default.
	if kaspi["payment_url"] != "https://pay.kaspi.kz/pay/xbp1imhd" || kaspi["recipient_name"] != "ТОО Jan Taxi" ||
		kaspi["recipient_bin"] != "000809500289" || instr["kk"] == "" || instr["ru"] == "" || instr["en"] == "" {
		t.Errorf("kaspi = %v", kaspi)
	}
	topup := cfg["topup"].(map[string]any)
	if topup["min"] != float64(500) || topup["max"] != float64(100000) || len(topup["presets"].([]any)) != 4 {
		t.Errorf("topup = %v", topup)
	}
	if _, ok := cfg["fare"]; ok {
		t.Error("driver config has fare")
	}

	_, cfg = getConfig(t, s, "?app=admin")
	for _, k := range []string{"fare", "kaspi", "topup"} {
		if _, ok := cfg[k]; ok {
			t.Errorf("admin config has %s", k)
		}
	}
	if cfg["city"] == nil {
		t.Error("admin config lacks city")
	}

	for _, q := range []string{"", "?app=bot", "?app=PASSENGER"} {
		rec, body := getConfig(t, s, q)
		if rec.Code != http.StatusUnprocessableEntity || fieldsOf(body)["app"] != reasonInvalid {
			t.Errorf("%q: %d %v", q, rec.Code, body)
		}
	}

	// Without an active city the config still renders, with city null and no fare.
	if _, err := pool.Exec(ctx, `UPDATE cities SET is_active = false`); err != nil {
		t.Fatal(err)
	}
	rec, cfg := getConfig(t, s, "?app=passenger")
	if rec.Code != http.StatusOK || cfg["city"] != nil {
		t.Errorf("no city: %d %v", rec.Code, cfg["city"])
	}
	if _, ok := cfg["fare"]; ok {
		t.Error("fare without city")
	}
}
