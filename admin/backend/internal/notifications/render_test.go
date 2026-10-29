package notifications

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// architectureKeys is the template list from ARCHITECTURE §8.
var architectureKeys = []string{
	"order_created_passenger", "order_available_driver", "order_accepted_passenger",
	"order_accepted_driver", "order_driver_arriving", "order_started", "order_stop_completed",
	"order_completed_passenger", "order_completed_driver", "order_cancelled_passenger",
	"order_cancelled_driver", "order_no_driver", "moderation_submitted", "moderation_review_required",
	"moderation_approved", "moderation_changes_requested", "moderation_rejected",
	"driver_suspended", "driver_restored", "topup_created", "topup_confirmed", "topup_rejected",
	"wallet_low_balance", "dispute_opened", "dispute_resolved", "phone_verified",
	// Business rules v2 (§22): the welcome bonus and the offer edits, which
	// replace the separate order_taken_driver message.
	"driver_welcome_bonus", "order_offer_won", "order_offer_taken", "order_offer_cancelled",
	// Admin controls (§22.7): a bonus credited by an operator.
	"driver_bonus_granted",
}

func TestRegistryCoversArchitecture(t *testing.T) {
	if got := len(Keys()); got != len(architectureKeys) {
		t.Fatalf("registry has %d templates, ARCHITECTURE lists %d", got, len(architectureKeys))
	}
	for _, k := range architectureKeys {
		tpl, ok := Lookup(k)
		if !ok {
			t.Fatalf("template %s missing", k)
		}
		if len(tpl.Bots) == 0 {
			t.Errorf("%s: no bots", k)
		}
		if tpl.Button == nil && !tpl.Edit {
			t.Errorf("%s: every message carries a web_app button (§8)", k)
		}
		used := map[string]bool{}
		for _, lang := range Languages {
			body := tpl.Bodies[lang]
			if err := tpl.ValidateBody(body); err != nil {
				t.Errorf("%s/%s: %v", k, lang, err)
			}
			names, _ := Placeholders(body)
			for _, n := range names {
				used[n] = true
			}
		}
		if tpl.Button != nil {
			names, err := Placeholders(tpl.Button.StartParam)
			if err != nil {
				t.Errorf("%s: start param: %v", k, err)
			}
			for _, n := range names {
				if _, ok := tpl.Param(n); !ok {
					t.Errorf("%s: start param uses undocumented {{%s}}", k, n)
				}
				used[n] = true
			}
		}
		if tpl.Callback != nil {
			names, err := Placeholders(tpl.Callback.Data)
			if err != nil {
				t.Errorf("%s: callback data: %v", k, err)
			}
			for _, n := range names {
				used[n] = true
			}
		}
		if tpl.Photo != "" {
			if p, ok := tpl.Param(tpl.Photo); !ok || p.Kind != KindFile {
				t.Errorf("%s: photo param %q must be a documented file param", k, tpl.Photo)
			}
			used[tpl.Photo] = true
		}
		for _, p := range tpl.Params {
			if !used[p.Name] {
				t.Errorf("%s: param %s is documented but never used", k, p.Name)
			}
			if p.Description == "" {
				t.Errorf("%s: param %s has no description", k, p.Name)
			}
		}
	}
}

func TestKazakhBodiesHaveNoRussianOnlyLetters(t *testing.T) {
	// ё, э, щ and ъ practically never occur in modern Kazakh; a Kazakh body
	// containing them almost certainly mixes in Russian.
	for _, k := range Keys() {
		tpl, _ := Lookup(k)
		texts := []string{tpl.Bodies[LangKK]}
		if tpl.Button != nil {
			texts = append(texts, tpl.Button.Label[LangKK])
		}
		for _, s := range texts {
			if strings.ContainsAny(strings.ToLower(s), "ёэщъ") {
				t.Errorf("%s: Kazakh text contains Russian-only letters: %q", k, s)
			}
		}
	}
}

func TestFormatMoney(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 ₸"},
		{50, "50 ₸"},
		{500, "500 ₸"},
		{1500, "1 500 ₸"},
		{100000, "100 000 ₸"},
		{1234567, "1 234 567 ₸"},
		{-2500, "-2 500 ₸"},
	}
	for _, tc := range tests {
		got := strings.ReplaceAll(FormatMoney(tc.in), nbsp, " ")
		if got != tc.want {
			t.Errorf("FormatMoney(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if !strings.Contains(FormatMoney(1500), "1"+nbsp+"500") {
		t.Errorf("money must use no-break spaces")
	}
}

func offerParams(orderID uuid.UUID) map[string]any {
	return map[string]any{
		"order_id":       orderID.String(),
		"order_number":   json.Number("1042"),
		"category":       "parcel",
		"vehicle_class":  map[string]any{"kk": "Седан", "ru": "Седан", "en": "Sedan"},
		"pickup":         `<script>alert("x")</script> & co`,
		"destination":    "Ембі\nбазар\t<b>",
		"route_distance": 5400,
		"route_duration": 700,
		"fare":           int64(1500),
		"payment_method": "cash",
	}
}

func TestRenderEscapesParams(t *testing.T) {
	tpl, _ := Lookup(TplOrderAvailableDriver)
	orderID := uuid.New()
	params := offerParams(orderID)
	msg, err := tpl.Render(LangKK, tpl.DefaultBody(LangKK), params, "https://app.example.kz/driver/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.Text, "<script>") || strings.Contains(msg.Text, "</script>") {
		t.Fatalf("param not escaped: %s", msg.Text)
	}
	for _, want := range []string{
		"&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; co",
		"Ембі базар &lt;b&gt;",
		"<b>Жаңа тапсырыс №1042</b>\nСәлемдеме · Седан",
		"<b>1" + nbsp + "500" + nbsp + "₸</b> · Қолма-қол",
		"Маршрут: 5,4" + nbsp + "км · ~12" + nbsp + "мин",
	} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("text %q does not contain %q", msg.Text, want)
		}
	}
	if err := ValidateHTML(msg.Text); err != nil {
		t.Fatalf("rendered text is not valid Telegram HTML: %v", err)
	}
	if strings.Contains(msg.Text, "Аялдамалар") || strings.Contains(msg.Text, "Жолаушылар") || strings.Contains(msg.Text, "A нүктесіне") {
		t.Fatalf("lines of absent optional params kept: %s", msg.Text)
	}
	if msg.Markup == nil || len(msg.Markup.InlineKeyboard) != 2 || len(msg.Markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("markup = %+v", msg.Markup)
	}
	if take := msg.Markup.InlineKeyboard[0][0]; take.Text != "✅ Алу" || take.CallbackData != "take:"+orderID.String() {
		t.Fatalf("take button = %+v", take)
	}
	btn := msg.Markup.InlineKeyboard[1][0]
	if btn.Text != "🗺 Картада ашу" || btn.WebApp == nil {
		t.Fatalf("button = %+v", btn)
	}
	if want := "https://app.example.kz/driver/?startapp=order_" + orderID.String(); btn.WebApp.URL != want {
		t.Fatalf("button url = %s, want %s", btn.WebApp.URL, want)
	}

	ru, err := tpl.Render(LangRU, tpl.DefaultBody(LangRU), params, "")
	if err != nil {
		t.Fatal(err)
	}
	// Without a Mini App URL only «Взять» remains.
	if !strings.Contains(ru.Text, "Новый заказ №1042</b>\nПосылка · Седан") || ru.Markup == nil ||
		len(ru.Markup.InlineKeyboard) != 1 || ru.Markup.InlineKeyboard[0][0].Text != "✅ Взять" {
		t.Fatalf("ru render = %q markup=%v", ru.Text, ru.Markup)
	}
}

func TestRenderOfferOptionalLinesAndPlaces(t *testing.T) {
	tpl, _ := Lookup(TplOrderAvailableDriver)
	p := offerParams(uuid.New())
	p["category"], p["pickup"], p["destination"] = "taxi", "", "48.826400, 58.144200"
	p["via"], p["pickup_distance"], p["passengers"], p["comment"], p["commission"] = "Базар → Мектеп", 850, 3, "2-подъезд", 50
	en, err := tpl.Render(LangEN, tpl.DefaultBody(LangEN), p, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"From: Point on the map", "To: Point on the map", "Stops: Базар → Мектеп", "To pickup: 850" + nbsp + "m",
		"Route: 5.4" + nbsp + "km · ~12" + nbsp + "min", "Passengers: 3", "Comment: 2-подъезд",
		"Commission: 50" + nbsp + "₸ (charged when you take it)", "Taxi · Sedan",
	} {
		if !strings.Contains(en.Text, want) {
			t.Errorf("text %q does not contain %q", en.Text, want)
		}
	}
	if strings.Contains(en.Text, "48.82") || strings.Contains(en.Text, "Cargo") {
		t.Fatalf("coordinates or cargo lines shown: %s", en.Text)
	}
	p["vehicle_class"] = "Sedan"
	if err := tpl.ValidateParams(p); err == nil {
		t.Fatal("a plain string accepted as a localized class name")
	}
}

func TestFormatDistanceAndDuration(t *testing.T) {
	for _, tc := range []struct {
		m    int64
		lang string
		want string
	}{{0, LangKK, "0 м"}, {850, LangKK, "850 м"}, {1000, LangKK, "1 км"}, {1250, LangRU, "1,3 км"},
		{5400, LangEN, "5.4 km"}, {123456, LangKK, "123 км"}} {
		if got := strings.ReplaceAll(FormatDistance(tc.m, tc.lang), nbsp, " "); got != tc.want {
			t.Errorf("FormatDistance(%d, %s) = %q, want %q", tc.m, tc.lang, got, tc.want)
		}
	}
	for _, tc := range []struct {
		s    int64
		lang string
		want string
	}{{0, LangKK, "~1 мин"}, {61, LangRU, "~2 мин"}, {600, LangEN, "~10 min"}} {
		if got := strings.ReplaceAll(FormatDuration(tc.s, tc.lang), nbsp, " "); got != tc.want {
			t.Errorf("FormatDuration(%d, %s) = %q, want %q", tc.s, tc.lang, got, tc.want)
		}
	}
}

func TestRenderTripCardLink(t *testing.T) {
	tpl, _ := Lookup(TplOrderAcceptedDriver)
	orderID := uuid.New()
	p := map[string]any{"order_id": orderID.String(), "order_number": 7, "pickup": "Абай 1", "destination": "",
		"fare": 1200, "payment_method": "bank_transfer", "passenger_name": "Айгүл <3",
		"passenger_link": "tg://user?id=42", "commission": 50}
	msg, err := tpl.Render(LangKK, tpl.DefaultBody(LangKK), p, "https://app.example.kz/driver/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Text, `Клиент: <a href="tg://user?id=42">Айгүл &lt;3</a>`) ||
		strings.Contains(msg.Text, "Телефон") || !strings.Contains(msg.Text, "Аударым") {
		t.Fatalf("trip card = %s", msg.Text)
	}
	if err := ValidateHTML(msg.Text); err != nil {
		t.Fatal(err)
	}
	if b := msg.Markup.InlineKeyboard[0][0]; b.WebApp == nil || !strings.HasSuffix(b.WebApp.URL, "startapp=order_"+orderID.String()) {
		t.Fatalf("button = %+v", b)
	}
	for _, bad := range []string{"javascript:alert(1)", "https://evil.example/x", "tg://user?id=abc", "https://t.me/a"} {
		p["passenger_link"] = bad
		if err := tpl.ValidateParams(p); err == nil {
			t.Errorf("link %q accepted", bad)
		}
	}
}

func TestRenderDropsLinesOfEmptyOptionalParams(t *testing.T) {
	tpl, _ := Lookup(TplOrderCancelledDriver)
	base := map[string]any{"order_number": 7}
	msg, err := tpl.Render(LangKK, tpl.DefaultBody(LangKK), base, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.Text, "Себебі") {
		t.Fatalf("empty reason line kept: %q", msg.Text)
	}
	want := "<b>Тапсырыс №7 жойылды</b>"
	if msg.Text != want {
		t.Fatalf("text = %q, want %q", msg.Text, want)
	}
	base["reason"] = "  Жолаушы  келмеді "
	msg, err = tpl.Render(LangKK, tpl.DefaultBody(LangKK), base, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(msg.Text, "\nСебебі: Жолаушы келмеді") {
		t.Fatalf("reason not rendered: %q", msg.Text)
	}
	base["refunded"] = 50
	msg, err = tpl.Render(LangKK, tpl.DefaultBody(LangKK), base, "")
	if err != nil || !strings.HasSuffix(msg.Text, "\nКомиссия қайтарылды: 50"+nbsp+"₸") {
		t.Fatalf("refund line: %q %v", msg.Text, err)
	}
}

func TestRenderUnsupportedLanguageFallsBackToKazakh(t *testing.T) {
	tpl, _ := Lookup(TplDriverRestored)
	msg, err := tpl.Render("de", tpl.DefaultBody("de"), nil, "https://x.example/driver")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg.Text, "<b>Аккаунт шектеуі") || msg.Markup.InlineKeyboard[0][0].Text != "Қосымшаны ашу" {
		t.Fatalf("fallback render = %q", msg.Text)
	}
}

func TestButtonURL(t *testing.T) {
	id := uuid.NewString()
	tests := []struct {
		name, base, pattern string
		params              map[string]any
		want                string
		ok                  bool
	}{
		{"plain", "https://a.kz/passenger/", "home", nil, "https://a.kz/passenger/?startapp=home", true},
		{"existing query", "https://a.kz/p?x=1", "orders", nil, "https://a.kz/p?startapp=orders&x=1", true},
		{"placeholder", "https://a.kz/p", "order_{{order_id}}", map[string]any{"order_id": id}, "https://a.kz/p?startapp=order_" + id, true},
		{"bad charset", "https://a.kz/p", "order_{{order_id}}", map[string]any{"order_id": "a b"}, "", false},
		{"missing value", "https://a.kz/p", "order_{{order_id}}", nil, "https://a.kz/p?startapp=order_", true},
		{"not a url", "javascript:alert(1)", "home", nil, "", false},
		{"plain http (dev)", "http://localhost:5173/passenger/", "home", nil, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := buttonURL(tc.base, tc.pattern, tc.params)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("buttonURL = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestValidateHTML(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"plain text", true},
		{"<b>bold</b> <i>it</i> <code>c</code>", true},
		{`<a href="https://t.me/x">link</a>`, true},
		{"&lt;tag&gt; &amp; &quot; &#39; &#x1F600;", true},
		{"<b><i>nested</i></b>", true},
		{"<b>unclosed", false},
		{"<b><i>bad</b></i>", false},
		{"<script>x</script>", false},
		{`<b class="x">x</b>`, false},
		{"a < b", false},
		{"a > b", false},
		{"Tom & Jerry", false},
		{"</b>", false},
		{`<a href="x" onclick="y">z</a>`, false},
	}
	for _, tc := range tests {
		if err := ValidateHTML(tc.in); (err == nil) != tc.ok {
			t.Errorf("ValidateHTML(%q) = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

func TestValidateBodyPlaceholders(t *testing.T) {
	tpl, _ := Lookup(TplTopUpConfirmed)
	tests := []struct {
		body string
		ok   bool
	}{
		{"Баланс: {{balance}} (+{{amount}})", true},
		{"{{reference}}", true},
		{"{{unknown}}", false},
		{"{{ balance }}", false},
		{"{{balance}", false},
		{"balance}}", false},
		{"{{Balance}}", false},
		{"   ", false},
		{strings.Repeat("а", MaxBodyLength+1), false},
		{strings.Repeat("а", MaxBodyLength), true},
		{"<b>{{amount}}", false},
	}
	for _, tc := range tests {
		if err := tpl.ValidateBody(tc.body); (err == nil) != tc.ok {
			name := tc.body
			if len(name) > 40 {
				name = name[:40] + "…"
			}
			t.Errorf("ValidateBody(%q) = %v, want ok=%v", name, err, tc.ok)
		}
	}
}

func TestValidateParams(t *testing.T) {
	tpl, _ := Lookup(TplOrderAvailableDriver)
	good := func() map[string]any {
		p := offerParams(uuid.New())
		p["category"], p["pickup"], p["destination"], p["fare"] = "taxi", "A", "B", int64(800)
		return p
	}
	if err := tpl.ValidateParams(good()); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown param", func(m map[string]any) { m["extra"] = 1 }},
		{"missing required", func(m map[string]any) { delete(m, "fare") }},
		{"empty required", func(m map[string]any) { m["payment_method"] = "  " }},
		{"bad payment", func(m map[string]any) { m["payment_method"] = "card" }},
		{"bad cargo size", func(m map[string]any) { m["cargo_size"] = "xxl" }},
		{"negative distance", func(m map[string]any) { m["route_distance"] = -1 }},
		{"money not integer", func(m map[string]any) { m["fare"] = 10.5 }},
		{"money string", func(m map[string]any) { m["fare"] = "abc" }},
		{"bad category", func(m map[string]any) { m["category"] = "boat" }},
		{"bad id", func(m map[string]any) { m["order_id"] = "x y" }},
		{"text not scalar", func(m map[string]any) { m["pickup"] = []string{"a"} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := good()
			tc.mutate(p)
			if err := tpl.ValidateParams(p); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	noParams, _ := Lookup(TplModerationSubmitted)
	if err := noParams.ValidateParams(nil); err != nil {
		t.Fatalf("nil params for a param-less template: %v", err)
	}
}

func TestCleanParam(t *testing.T) {
	long := strings.Repeat("ә", maxTextParam+10)
	tests := []struct{ in, want string }{
		{"  a \n\n b\tc  ", "a b c"},
		{"x‮y⁦z\x00", "xyz"},
		{long, strings.Repeat("ә", maxTextParam) + "…"},
		{"\n\t ", ""},
	}
	for _, tc := range tests {
		if got := cleanParam(tc.in); got != tc.want {
			t.Errorf("cleanParam(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeErrorRedactsTokens(t *testing.T) {
	err := errorString("post https://api.telegram.org/bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw/sendMessage: timeout")
	got := sanitizeError(err)
	if strings.Contains(got, "AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw") {
		t.Fatalf("token leaked: %s", got)
	}
	long := sanitizeError(errorString(strings.Repeat("ж", 400)))
	if len(long) > maxErrorText || !strings.HasPrefix(long, "ж") {
		t.Fatalf("truncation broke text: len=%d", len(long))
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

// TestBonusGrantedMessage: the manual bonus message names the company, shows
// the comment only when given and opens the wallet (§22.7).
func TestBonusGrantedMessage(t *testing.T) {
	tpl, _ := Lookup(TplDriverBonusGranted)
	url := "https://example.kz/driver/"
	want := map[string]string{
		LangKK: "Jan Taxi компаниясынан сізге 1\u00a0000\u00a0₸ бонус есептелді",
		LangRU: "Компания Jan Taxi начислила вам бонус 1\u00a0000\u00a0₸",
		LangEN: "Jan Taxi has credited you a bonus of 1\u00a0000\u00a0₸",
	}
	for lang, head := range want {
		msg, err := tpl.Render(lang, tpl.DefaultBody(lang), map[string]any{"amount": 1000, "balance": 1450, "comment": "Жақсы жұмыс үшін"}, url)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(msg.Text, head) || !strings.Contains(msg.Text, "Жақсы жұмыс үшін") || !strings.Contains(msg.Text, "1\u00a0450") {
			t.Fatalf("%s: %q", lang, msg.Text)
		}
		if msg.Markup == nil || msg.Markup.InlineKeyboard[0][0].WebApp == nil ||
			!strings.HasSuffix(msg.Markup.InlineKeyboard[0][0].WebApp.URL, "startapp=wallet") {
			t.Fatalf("%s: button %+v", lang, msg.Markup)
		}
		plain, err := tpl.Render(lang, tpl.DefaultBody(lang), map[string]any{"amount": 1000, "balance": 1450}, url)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(plain.Text, "\n") != 1 {
			t.Fatalf("%s without comment: %q", lang, plain.Text)
		}
	}
}

// TestRenderLocalityLine: the offer and the accepted-order message name the
// settlement of the pickup («📍 Алматы»); without one the line is left out.
func TestRenderLocalityLine(t *testing.T) {
	offer, _ := Lookup(TplOrderAvailableDriver)
	p := offerParams(uuid.New())
	p["locality"] = "Алматы"
	for lang, want := range map[string]string{
		LangKK: "Сәлемдеме · Седан\n📍 Алматы\nҚайдан:",
		LangRU: "Посылка · Седан\n📍 Алматы\nОткуда:",
		LangEN: "Parcel · Sedan\n📍 Алматы\nFrom:",
	} {
		msg, err := offer.Render(lang, offer.DefaultBody(lang), p, "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(msg.Text, want) {
			t.Errorf("%s offer %q lacks %q", lang, msg.Text, want)
		}
	}
	delete(p, "locality")
	msg, err := offer.Render(LangKK, offer.DefaultBody(LangKK), p, "")
	if err != nil || strings.Contains(msg.Text, "📍") {
		t.Fatalf("offer without locality: %q %v", msg.Text, err)
	}

	trip, _ := Lookup(TplOrderAcceptedDriver)
	tp := map[string]any{"order_id": uuid.NewString(), "order_number": 7, "pickup": "Абай 1", "destination": "Базар",
		"fare": 1200, "payment_method": "cash", "passenger_name": "Айгүл", "passenger_link": "tg://user?id=42",
		"commission": 50, "locality": "Ақтөбе <қ>"}
	msg, err = trip.Render(LangKK, trip.DefaultBody(LangKK), tp, "")
	if err != nil || !strings.Contains(msg.Text, "</b>\n📍 Ақтөбе &lt;қ&gt;\nҚайдан: Абай 1") {
		t.Fatalf("trip card: %q %v", msg.Text, err)
	}
	if err := ValidateHTML(msg.Text); err != nil {
		t.Fatal(err)
	}
}
