package routing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// recorder is an httptest server that answers by path and records queries.
type recorder struct {
	srv     *httptest.Server
	mu      sync.Mutex
	queries []url.Values
	paths   []string
	headers []http.Header
}

func newRecorder(t *testing.T, answer func(path string, q url.Values) (int, string)) *recorder {
	t.Helper()
	rec := &recorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.queries = append(rec.queries, r.URL.Query())
		rec.paths = append(rec.paths, r.URL.Path)
		rec.headers = append(rec.headers, r.Header.Clone())
		rec.mu.Unlock()
		status, body := answer(r.URL.Path, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (r *recorder) last() (string, url.Values, http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.paths) - 1
	return r.paths[n], r.queries[n], r.headers[n]
}

const photonSearchBody = `{"type":"FeatureCollection","features":[
 {"type":"Feature","geometry":{"type":"Point","coordinates":[58.1504,48.8291]},
  "properties":{"name":"Ембі темір жол вокзалы","street":"Темір жол көшесі","housenumber":"1","city":"Ембі","state":"Ақтөбе облысы","countrycode":"KZ","osm_key":"railway"}},
 {"type":"Feature","geometry":{"type":"Point","coordinates":[58.1442,48.8264]},
  "properties":{"street":"Жиенқали Тілепбергенов көшесі","housenumber":"12","city":"Ембі","state":"Ақтөбе облысы","countrycode":"KZ"}},
 {"type":"Feature","geometry":{"type":"Point","coordinates":[58.15,48.83]},
  "properties":{"city":"Ембі","county":"Мұғалжар ауданы","state":"Ақтөбе облысы","countrycode":"KZ"}},
 {"type":"Feature","geometry":{"type":"Polygon","coordinates":[]},"properties":{"name":"skip"}},
 {"type":"Feature","geometry":{"type":"Point","coordinates":[58.15,48.83]},"properties":{}}
]}`

func TestPhoton(t *testing.T) {
	rec := newRecorder(t, func(path string, q url.Values) (int, string) {
		switch path {
		case "/api":
			return 200, photonSearchBody
		case "/reverse":
			if q.Get("lat") == "48.000000" {
				return 200, `{"type":"FeatureCollection","features":[]}`
			}
			return 200, photonSearchBody
		}
		return 500, `oops`
	})
	g, err := NewPhoton(rec.srv.URL+"/", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	places, err := g.Search(ctx, "вокзал", &embi, "kk", 7)
	if err != nil {
		t.Fatal(err)
	}
	path, q, hdr := rec.last()
	if path != "/api" || q.Get("q") != "вокзал" || q.Get("countrycode") != "KZ" || q.Get("lang") != "default" ||
		q.Get("limit") != "7" || q.Get("lat") != "48.826400" || q.Get("lon") != "58.144200" || hdr.Get("User-Agent") == "" {
		t.Fatalf("request %s %v", path, q)
	}
	want := []Place{
		{Title: "Ембі темір жол вокзалы", Subtitle: "Темір жол көшесі, 1, Ембі, Ақтөбе облысы", Point: embiStation, Source: "photon", CountryCode: "KZ"},
		{Title: "Жиенқали Тілепбергенов көшесі, 12", Subtitle: "Ембі, Ақтөбе облысы", Point: embi, Source: "photon", CountryCode: "KZ"},
		{Title: "Ембі", Subtitle: "Мұғалжар ауданы, Ақтөбе облысы", Point: geo.Point{Lat: 48.83, Lon: 58.15}, Source: "photon", CountryCode: "KZ"},
	}
	if len(places) != len(want) {
		t.Fatalf("places = %+v", places)
	}
	for i := range want {
		if places[i] != want[i] {
			t.Errorf("place %d = %+v, want %+v", i, places[i], want[i])
		}
	}
	if _, err := g.Search(ctx, "station", nil, "en", 5); err != nil {
		t.Fatal(err)
	}
	if _, q, _ := rec.last(); q.Get("lang") != "en" || q.Has("lat") {
		t.Fatalf("en search %v", q)
	}
	if _, err := g.Search(ctx, "вокзал", nil, "ru", 5); err != nil {
		t.Fatal(err)
	}
	if _, q, _ := rec.last(); q.Get("lang") != "default" {
		t.Fatalf("ru search must use local names: %v", q)
	}

	p, err := g.Reverse(ctx, embi, "kk")
	if err != nil || p == nil || p.Title != "Ембі темір жол вокзалы" {
		t.Fatalf("reverse = %+v %v", p, err)
	}
	if path, q, _ := rec.last(); path != "/reverse" || q.Get("limit") != "1" || q.Get("radius") == "" {
		t.Fatalf("reverse request %s %v", path, q)
	}
	if p, err := g.Reverse(ctx, geo.Point{Lat: 48, Lon: 58}, "kk"); err != nil || p != nil {
		t.Fatalf("empty reverse = %+v %v", p, err)
	}

	bad, _ := NewPhoton(rec.srv.URL+"/broken", http.DefaultClient)
	_, err = bad.Search(ctx, "x", nil, "kk", 5)
	var pe *providerError
	if !errors.As(err, &pe) {
		t.Fatalf("HTTP 500 = %v", err)
	}
}

func TestNominatim(t *testing.T) {
	if _, err := NewNominatim("https://nominatim.openstreetmap.org", http.DefaultClient); err == nil {
		t.Fatal("public Nominatim accepted")
	}
	if _, _, err := NewProvider(ProviderConfig{Provider: "nominatim", BaseURL: "https://nominatim.openstreetmap.org/"}); err == nil {
		t.Fatal("public Nominatim accepted through NewProvider")
	}
	rec := newRecorder(t, func(path string, q url.Values) (int, string) {
		switch path {
		case "/search":
			return 200, `[{"lat":"48.8291","lon":"58.1504","name":"Вокзал","display_name":"Вокзал, Эмба, Актюбинская область, Казахстан",
				"address":{"road":"Вокзальная улица","house_number":"1","town":"Эмба","state":"Актюбинская область","country_code":"kz"}},
				{"lat":"x","lon":"58","name":"broken"}]`
		case "/reverse":
			if q.Get("lat") == "48.000000" {
				return 200, `{"error":"Unable to geocode"}`
			}
			return 200, `{"lat":"48.8264","lon":"58.1442","name":"","display_name":"12, улица Тлепбергенова, Эмба",
				"address":{"road":"улица Тлепбергенова","house_number":"12","town":"Эмба","state":"Актюбинская область","country_code":"kz"}}`
		}
		return 404, `{}`
	})
	g, err := NewNominatim(rec.srv.URL, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	places, err := g.Search(ctx, "вокзал", &embi, "ru", 5)
	if err != nil || len(places) != 1 {
		t.Fatalf("search = %+v %v", places, err)
	}
	if p := places[0]; p.Title != "Вокзал" || p.Subtitle != "Вокзальная улица, 1, Эмба, Актюбинская область" || p.CountryCode != "KZ" || p.Point != embiStation {
		t.Fatalf("place = %+v", p)
	}
	_, q, _ := rec.last()
	if q.Get("countrycodes") != "kz" || q.Get("accept-language") != "ru,kk" || q.Get("format") != "jsonv2" ||
		q.Get("viewbox") != "57.844200,49.126400,58.444200,48.526400" || q.Get("bounded") != "0" {
		t.Fatalf("search query %v", q)
	}
	p, err := g.Reverse(ctx, embi, "kk")
	if err != nil || p == nil || p.Title != "улица Тлепбергенова, 12" || p.Subtitle != "Эмба, Актюбинская область" {
		t.Fatalf("reverse = %+v %v", p, err)
	}
	if _, q, _ := rec.last(); q.Get("accept-language") != "kk,ru" || q.Get("zoom") != "18" {
		t.Fatalf("reverse query %v", q)
	}
	if p, err := g.Reverse(ctx, geo.Point{Lat: 48, Lon: 58}, "en"); err != nil || p != nil {
		t.Fatalf("unable to geocode = %+v %v", p, err)
	}
}

func TestTwoGIS(t *testing.T) {
	if _, err := NewTwoGIS("https://catalog.api.2gis.com", "", embi, http.DefaultClient); err == nil {
		t.Fatal("missing key accepted")
	}
	suggestsEmpty := false
	rec := newRecorder(t, func(path string, q url.Values) (int, string) {
		switch {
		case path == "/3.0/suggests" && suggestsEmpty:
			return 200, `{"meta":{"code":200},"result":{"items":[{"name":"Вокзал","type":"rubric"}],"total":1}}`
		case path == "/3.0/suggests":
			return 200, `{"meta":{"api_version":"3.0","code":200},"result":{"items":[
				{"name":"Ембі вокзалы","address_name":"Теміржол көшесі, 1","point":{"lat":48.8291,"lon":58.1504},
				 "adm_div":[{"name":"Қазақстан","type":"country"},{"name":"Ақтөбе облысы","type":"region"},{"name":"Ембі","type":"city"}]},
				{"name":"no point"}],"total":2}}`
		case path == "/3.0/items/geocode" && q.Get("q") == "nothing":
			return 404, `{"meta":{"code":404,"error":{"type":"itemNotFound","message":"Results not found"}}}`
		case path == "/3.0/items/geocode" && q.Get("q") == "forbidden":
			return 200, `{"meta":{"code":403,"error":{"type":"keyIsBlocked","message":"Key is blocked"}}}`
		case path == "/3.0/items/geocode":
			return 200, `{"meta":{"code":200},"result":{"items":[
				{"name":"Тілепбергенов көшесі, 12","full_name":"Ембі, Тілепбергенов көшесі, 12","point":{"lat":48.8264,"lon":58.1442},
				 "adm_div":[{"name":"Ембі","type":"settlement"}]}]}}`
		}
		return 500, `{}`
	})
	g, err := NewTwoGIS(rec.srv.URL, "secret-key", embi, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	places, err := g.Search(ctx, "вокзал", nil, "kk", 5)
	if err != nil || len(places) != 1 {
		t.Fatalf("suggests = %+v %v", places, err)
	}
	if p := places[0]; p.Title != "Ембі вокзалы" || p.Subtitle != "Теміржол көшесі, 1, Ембі, Ақтөбе облысы" || p.Source != "twogis" || p.Point != embiStation {
		t.Fatalf("place = %+v", p)
	}
	path, q, _ := rec.last()
	if path != "/3.0/suggests" || q.Get("key") != "secret-key" || q.Get("locale") != "kk_KZ" || q.Get("location") != "58.144200,48.826400" ||
		q.Get("suggest_type") != "route_endpoint" || !strings.Contains(q.Get("fields"), "items.point") || q.Get("page_size") != "5" {
		t.Fatalf("suggests request %s %v", path, q)
	}

	// Suggestions without coordinates fall back to the geocoder.
	suggestsEmpty = true
	places, err = g.Search(ctx, "Тілепбергенов 12", &embiBazaar, "en", 3)
	if err != nil || len(places) != 1 || places[0].Title != "Тілепбергенов көшесі, 12" || places[0].Subtitle != "Ембі" {
		t.Fatalf("geocode fallback = %+v %v", places, err)
	}
	path, q, _ = rec.last()
	if path != "/3.0/items/geocode" || q.Get("locale") != "ru_KZ" || q.Get("point") != "58.141800,48.824700" || q.Get("radius") == "" {
		t.Fatalf("geocode request %s %v", path, q)
	}
	if places, err := g.Search(ctx, "nothing", nil, "ru", 3); err != nil || len(places) != 0 {
		t.Fatalf("not found = %+v %v", places, err)
	}
	if _, err := g.Search(ctx, "forbidden", nil, "ru", 3); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("meta 403 = %v", err)
	}

	p, err := g.Reverse(ctx, embi, "ru")
	if err != nil || p == nil || p.Title != "Тілепбергенов көшесі, 12" {
		t.Fatalf("reverse = %+v %v", p, err)
	}
	if path, q, _ := rec.last(); path != "/3.0/items/geocode" || q.Get("lat") != "48.826400" || q.Get("lon") != "58.144200" || q.Has("q") {
		t.Fatalf("reverse request %s %v", path, q)
	}

	// Transport errors never carry the key-bearing URL.
	rec.srv.Close()
	_, err = g.Search(ctx, "x", nil, "kk", 1)
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("closed server error leaks the key: %v", err)
	}
}

func TestNewProvider(t *testing.T) {
	for _, tt := range []struct {
		cfg     ProviderConfig
		name    string
		wantErr bool
	}{
		{ProviderConfig{}, "none", false},
		{ProviderConfig{Provider: "none"}, "none", false},
		{ProviderConfig{Provider: "photon", BaseURL: "http://photon:2322"}, "photon", false},
		{ProviderConfig{Provider: "photon", BaseURL: "photon:2322"}, "photon", true},
		{ProviderConfig{Provider: "nominatim", BaseURL: "http://nominatim:8080"}, "nominatim", false},
		{ProviderConfig{Provider: "twogis", BaseURL: "https://catalog.api.2gis.com", APIKey: "k", DefaultBias: embi}, "twogis", false},
		{ProviderConfig{Provider: "twogis", BaseURL: "https://catalog.api.2gis.com"}, "twogis", true},
		{ProviderConfig{Provider: "google"}, "", true},
	} {
		g, name, err := NewProvider(tt.cfg)
		if (err != nil) != tt.wantErr || (err == nil && (g == nil || name != tt.name)) {
			t.Errorf("%+v: %v %q %v", tt.cfg, g, name, err)
		}
	}
}

func TestBreakerAndCache(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := newBreaker(2, 30*time.Second)
	b.now = func() time.Time { return now }
	if !b.allow() {
		t.Fatal("closed breaker rejects")
	}
	b.failure()
	if !b.allow() || b.open() {
		t.Fatal("opened after one failure")
	}
	b.failure()
	if b.allow() || !b.open() {
		t.Fatal("not open after threshold")
	}
	now = now.Add(31 * time.Second)
	if !b.allow() {
		t.Fatal("no probe after cooldown")
	}
	if b.allow() {
		t.Fatal("second concurrent probe allowed")
	}
	b.abort()
	if !b.allow() {
		t.Fatal("aborted probe blocks the breaker")
	}
	b.failure()
	if b.allow() {
		t.Fatal("failed probe did not re-open")
	}
	now = now.Add(31 * time.Second)
	if !b.allow() {
		t.Fatal("no probe after second cooldown")
	}
	b.success()
	if !b.allow() || !b.allow() || b.open() {
		t.Fatal("success did not close the breaker")
	}

	c := newTTLCache[string, int](time.Minute, 2)
	c.now = func() time.Time { return now }
	c.set("a", 1)
	c.set("b", 2)
	c.set("a", 3) // refresh moves a to the back
	c.set("c", 4) // evicts b, the oldest
	if v, ok := c.get("a"); !ok || v != 3 {
		t.Fatalf("a = %v %v", v, ok)
	}
	if _, ok := c.get("b"); ok {
		t.Fatal("b not evicted")
	}
	now = now.Add(time.Minute)
	if _, ok := c.get("c"); ok || c.len() != 1 {
		t.Fatalf("expired entry served (len %d)", c.len())
	}
	var nilCache *ttlCache[string, int]
	nilCache.set("x", 1)
	if _, ok := nilCache.get("x"); ok {
		t.Fatal("nil cache returned a value")
	}
}

func TestDedupeAndLabels(t *testing.T) {
	near := geo.Point{Lat: embiStation.Lat + 0.0001, Lon: embiStation.Lon} // ~11 m
	sameName := geo.Point{Lat: embiStation.Lat + 0.002, Lon: embiStation.Lon}
	far := geo.Point{Lat: embiStation.Lat + 0.01, Lon: embiStation.Lon}
	in := []Place{
		{Title: "Теміржол вокзалы", Point: embiStation, Source: "place"},
		{Title: "Ембі вокзалы", Point: near, Source: "photon"},
		{Title: "ТЕМІРЖОЛ  вокзалы", Point: sameName, Source: "photon"},
		{Title: "Теміржол вокзалы", Point: far, Source: "photon"},
		{Title: "Орталық базар", Point: embiBazaar, Source: "photon"},
	}
	out := dedupe(in)
	if len(out) != 3 || out[0].Source != "place" || out[1].Point != far || out[2].Title != "Орталық базар" {
		t.Fatalf("dedupe = %+v", out)
	}
	if got := joinParts([]string{"Ембі"}, "", "Ембі", "Ақтөбе облысы", "ақтөбе облысы", " "); got != "Ақтөбе облысы" {
		t.Fatalf("joinParts = %q", got)
	}
	if got := coordinatesPlace(geo.Point{Lat: 48.8264, Lon: 58.1442}, "x").Title; got != "48.826400, 58.144200" {
		t.Fatalf("coordinates title = %q", got)
	}
}
