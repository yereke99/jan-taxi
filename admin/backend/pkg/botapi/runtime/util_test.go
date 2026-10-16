package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/pkg/telegram"
)

type staticHealth Health

func (s staticHealth) Health() Health { return Health(s) }

func TestHealthEndpoint(t *testing.T) {
	age := 3.5
	srv := httptest.NewServer(HealthHandler(staticHealth{Role: RoleLeader, Healthy: true, LastPollAgeS: &age}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != 200 || body["role"] != "leader" || body["last_poll_age_s"] != 3.5 {
		t.Fatalf("healthy: %d %v", resp.StatusCode, body)
	}
	if err := CheckHealth(context.Background(), srv.URL+"/healthz"); err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}

	bad := httptest.NewServer(HealthHandler(staticHealth{Role: RoleLeader}))
	defer bad.Close()
	resp, err = http.Get(bad.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy: %d", resp.StatusCode)
	}
	if err := CheckHealth(context.Background(), bad.URL+"/healthz"); err == nil {
		t.Fatal("CheckHealth must fail on 503")
	}
	if err := CheckHealth(context.Background(), "http://127.0.0.1:1/healthz"); err == nil {
		t.Fatal("CheckHealth must fail when nothing listens")
	}
}

func TestPollerHealthStates(t *testing.T) {
	e := newPollerEnv(t)
	p := e.poller(t, "bot-a", (&recorder{}).handler(), nil)
	if h := p.Health(); h.Healthy || h.Role != RoleStandby {
		t.Fatalf("not running: %+v", h)
	}
	stop := start(t, p)
	waitFor(t, 3*time.Second, "healthy leader", func() bool {
		h := p.Health()
		return h.Healthy && h.Role == RoleLeader && h.LastPollAgeS != nil
	})
	// A leader whose last successful poll is older than the window is unhealthy.
	stop()
	p.running.Store(true) // inspect the state of a live leader without polling
	p.mu.Lock()
	p.leader = true
	p.lastPoll = time.Now().Add(-2 * HealthWindow)
	p.lastProgress, p.leaderSince = p.lastPoll, p.lastPoll
	p.mu.Unlock()
	if h := p.Health(); h.Healthy || h.Role != RoleLeader || h.LastPollAgeS == nil || *h.LastPollAgeS < HealthWindow.Seconds() {
		t.Fatalf("stale leader: %+v", h)
	}
	p.mu.Lock()
	p.lastProgress = time.Now()
	p.mu.Unlock()
	if h := p.Health(); !h.Healthy {
		t.Fatalf("leader that just processed an update: %+v", h)
	}
	p.running.Store(false)
}

func TestServeHealthOnStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeHealthOn(ctx, ln, staticHealth{Healthy: true, Role: RoleStandby}, nil) }()
	waitFor(t, 2*time.Second, "health server", func() bool {
		return CheckHealth(context.Background(), "http://"+ln.Addr().String()+"/healthz") == nil
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeHealthOn: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("health server did not stop")
	}
}

func TestSendHTMLRetriesFloodWaitAndOmitsNilMarkup(t *testing.T) {
	tg := newFakeTelegram(t)
	tg.sendFailures = 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var kb *telegram.InlineKeyboardMarkup
	begin := time.Now()
	if err := SendHTML(ctx, tg.client(), 42, "<b>Сәлем</b> &amp;", kb); err != nil {
		t.Fatal(err)
	}
	if time.Since(begin) < 950*time.Millisecond {
		t.Fatal("429 retry_after not honoured")
	}
	tg.mu.Lock()
	sent := append([]map[string]any(nil), tg.sent...)
	tg.sendFailures = 1
	tg.mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	if _, ok := sent[0]["reply_markup"]; ok {
		t.Fatalf("nil markup sent: %v", sent[0])
	}
	if sent[0]["parse_mode"] != "HTML" || sent[0]["chat_id"] != float64(42) {
		t.Fatalf("message: %v", sent[0])
	}

	// A flood wait longer than the remaining reply budget is not waited for.
	short, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if err := SendHTML(short, tg.client(), 42, "x", nil); err == nil {
		t.Fatal("expected the 429 to be returned when the deadline is too close")
	}
}

func TestMatcher(t *testing.T) {
	m := NewMatcher()
	if err := m.Add("taxi", "🚕 Такси шақыру", "🚕 Вызвать такси", "🚕 Order a taxi"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("balance", "💰 Баланс", "💰 Balance"); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("balance", "💰 Баланс"); err != nil {
		t.Fatalf("same label for the same action (kk and ru) must be allowed: %v", err)
	}
	if err := m.Add("other", "Баланс"); err == nil {
		t.Fatal("conflicting label accepted")
	}
	for text, want := range map[string]string{
		"🚕 Такси шақыру":    "taxi",
		"такси шақыру":      "taxi",
		"  ВЫЗВАТЬ  ТАКСИ ": "taxi",
		"Order a taxi!":     "taxi",
		"💰Баланс":           "balance",
	} {
		if got, ok := m.Match(text); !ok || got != want {
			t.Errorf("Match(%q) = %q, %v; want %q", text, got, ok, want)
		}
	}
	if _, ok := m.Match("сәлем"); ok {
		t.Fatal("unknown text matched")
	}
	// Decomposed input (и + combining breve) matches the composed label "й".
	if err := m.Add("profile", "👤 Мой профиль"); err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Match("Мои\u0306 профиль"); !ok || got != "profile" {
		t.Fatalf("NFC match: %q %v", got, ok)
	}
}

func TestCatalog(t *testing.T) {
	good := Catalog{
		"kk": {"hi": "Сәлем, %s!", "n": "%d ₸"},
		"ru": {"hi": "Привет, %s!", "n": "%d ₸"},
		"en": {"hi": "Hi, %s!", "n": "%d ₸"},
	}
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	if got := good.T("ru", "hi", "Әлия"); got != "Привет, Әлия!" {
		t.Fatalf("T: %q", got)
	}
	if got := good.T("de", "hi", "A"); got != "Сәлем, A!" {
		t.Fatalf("unknown language falls back to kk: %q", got)
	}
	if got := good.T("kk", "missing"); got != "missing" {
		t.Fatalf("missing key: %q", got)
	}
	bad := Catalog{
		"kk": {"hi": "Сәлем, %s!", "n": "%d"},
		"ru": {"hi": "Привет!"},
		"en": {"hi": "Hi, %s!", "n": "%d", "extra": "x"},
	}
	err := bad.Check()
	if err == nil {
		t.Fatal("bad catalog accepted")
	}
	for _, want := range []string{`ru: missing "n"`, `en: unknown key "extra"`, `ru: "hi" uses`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Check error %q lacks %q", err, want)
		}
	}
}

func TestKeyboards(t *testing.T) {
	if got := MiniAppURL("https://app.jantaxi.kz/passenger/", "order_0a6f4c4e-6f1b-4b8e-9a52-3c1d2e7f9b10"); got != "https://app.jantaxi.kz/passenger/?startapp=order_0a6f4c4e-6f1b-4b8e-9a52-3c1d2e7f9b10" {
		t.Fatalf("MiniAppURL: %s", got)
	}
	if got := MiniAppURL("https://app.jantaxi.kz/driver/?v=2", "topup"); got != "https://app.jantaxi.kz/driver/?startapp=topup&v=2" {
		t.Fatalf("MiniAppURL keeps query: %s", got)
	}
	if got := MiniAppURL("https://app.jantaxi.kz/driver/", "bad param!"); got != "https://app.jantaxi.kz/driver/" {
		t.Fatalf("invalid param: %s", got)
	}
	if MiniAppURL("", "taxi") != "" {
		t.Fatal("empty base")
	}
	kb := Inline(
		[]telegram.InlineKeyboardButton{WebAppButton("Open", "http://localhost:5173/passenger/")},
		[]telegram.InlineKeyboardButton{WebAppButton("Open", "https://app.jantaxi.kz/passenger/"), CallbackButton("x", "")},
		[]telegram.InlineKeyboardButton{URLButton("Bot", BotLink("@jantaxi_driver_bot", "register"))},
	)
	if kb == nil || len(kb.InlineKeyboard) != 2 || kb.InlineKeyboard[0][0].WebApp.URL != "https://app.jantaxi.kz/passenger/" ||
		kb.InlineKeyboard[1][0].URL != "https://t.me/jantaxi_driver_bot?start=register" {
		t.Fatalf("inline keyboard: %+v", kb)
	}
	if Inline([]telegram.InlineKeyboardButton{WebAppButton("x", "http://insecure")}) != nil {
		t.Fatal("empty keyboard must be nil")
	}
	rk := ReplyKeyboard("", Row("a", "b"), nil, ContactRow("phone"))
	if len(rk.Keyboard) != 2 || !rk.IsPersistent || !rk.ResizeKeyboard || !rk.Keyboard[1][0].RequestContact {
		t.Fatalf("reply keyboard: %+v", rk)
	}
	b, _ := json.Marshal(rk)
	if strings.Contains(string(b), "web_app") {
		t.Fatal("reply keyboards must never carry web_app buttons")
	}
}

func TestThrottle(t *testing.T) {
	th := NewThrottle(10 * time.Minute)
	now := time.Now()
	th.now = func() time.Time { return now }
	if !th.Allow(1) || th.Allow(1) || !th.Allow(2) {
		t.Fatal("first event per key only")
	}
	now = now.Add(10 * time.Minute)
	if !th.Allow(1) {
		t.Fatal("allowed again after the interval")
	}
	th.Reset(1)
	if !th.Allow(1) {
		t.Fatal("reset")
	}
}

func TestProfileApply(t *testing.T) {
	tg := newFakeTelegram(t)
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "setChatMenuButton") && !strings.Contains(string(b), `"type":"web_app"`) {
			t.Errorf("menu button body: %s", b)
		}
		writeTG(w, 200, `{"ok":true,"result":true}`)
	}))
	defer srv.Close()
	_ = tg
	c := telegram.New(testToken, telegram.WithBaseURL(srv.URL))
	err := ApplyProfile(context.Background(), c, Profile{
		Commands:          map[string][]telegram.BotCommand{"": {{Command: "start", Description: "Бастау"}}, "ru": {{Command: "start", Description: "Начать"}}},
		Descriptions:      map[string]string{"": "Сипаттама"},
		ShortDescriptions: map[string]string{"en": "Short"},
		MenuButton:        &telegram.MenuButtonWebApp{Text: "Jan Taxi", WebApp: telegram.WebAppInfo{URL: "https://app.jantaxi.kz/passenger/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"setMyCommands", "setMyCommands", "setMyDescription", "setMyShortDescription", "setChatMenuButton"}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", methods, want)
	}
}
