package routing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

var quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func addPlace(t *testing.T, pool *pgxpool.Pool, kk, ru, en string, p geo.Point, aliases ...string) {
	t.Helper()
	if aliases == nil {
		aliases = []string{}
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO places (city_id, name_kk, name_ru, name_en, aliases, lat, lon)
		VALUES ((SELECT id FROM cities WHERE code = 'EMB'), $1, $2, $3, $4, $5, $6)`,
		kk, ru, en, aliases, p.Lat, p.Lon); err != nil {
		t.Fatal(err)
	}
}

func TestInKazakhstanBatch(t *testing.T) {
	pool := testutil.NewDB(t)
	got, err := inKazakhstan(context.Background(), pool, []Place{
		{Point: embi}, {Point: orenburg}, {Point: geo.Point{Lat: 43.2389, Lon: 76.8897}}, // Almaty
		{Point: geo.Point{Lat: 41.2995, Lon: 69.2401}}, // Tashkent
		{Point: geo.Point{Lat: 95, Lon: 0}},            // invalid
		{Point: geo.Point{Lat: 45.6167, Lon: 63.3167}}, // Baikonur
	})
	want := []bool{true, false, true, false, false, true}
	if err != nil || len(got) != len(want) {
		t.Fatalf("inKazakhstan = %v %v", got, err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d: got %v, want %v", i, got[i], want[i])
		}
	}
	if got, err := inKazakhstan(context.Background(), pool, nil); err != nil || len(got) != 0 {
		t.Fatalf("empty = %v %v", got, err)
	}
}

func TestPlacesSearchChain(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	addPlace(t, pool, "Теміржол вокзалы", "Железнодорожный вокзал", "Railway station", embiStation, "вокзал")
	prov := &fakeGeocoder{search: []Place{
		{Title: "Оренбург вокзалы", Point: orenburg, Source: "photon", CountryCode: "RU"},
		{Title: "Ембі станциясы", Point: geo.Point{Lat: embiStation.Lat + 0.0001, Lon: embiStation.Lon}, Source: "photon"},
		{Title: "Вокзал көшесі, 3", Subtitle: "Ембі", Point: embiSchool, Source: "photon", CountryCode: "KZ"},
	}}
	s := NewPlaces(pool, PlacesConfig{Provider: prov, ProviderName: "photon", Log: quietLog})

	got, err := s.Search(ctx, "вокзал", &embi, "kk", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("results = %+v", got)
	}
	if got[0].Source != "place" || got[0].Title != "Теміржол вокзалы" || got[0].Subtitle != "Ембі" {
		t.Fatalf("curated place not first: %+v", got[0])
	}
	if got[1].Title != "Вокзал көшесі, 3" {
		t.Fatalf("provider result = %+v", got[1])
	}
	// Language follows the request.
	got, err = s.Search(ctx, "вокзал", &embi, "ru", 10)
	if err != nil || got[0].Title != "Железнодорожный вокзал" || got[0].Subtitle != "Эмба" {
		t.Fatalf("ru = %+v %v", got, err)
	}

	// The provider result for the same query is cached.
	if n, _ := prov.counts(); n != 2 { // kk and ru differ
		t.Fatalf("provider searches = %d", n)
	}
	if _, err := s.Search(ctx, "ВОКЗАЛ ", &embi, "kk", 10); err != nil {
		t.Fatal(err)
	}
	if n, _ := prov.counts(); n != 2 {
		t.Fatalf("cache missed: %d searches", n)
	}

	// Limit applies after merging; enough curated hits skip the provider.
	addPlace(t, pool, "Вокзал алаңы", "Привокзальная площадь", "Station square", embiAkimat)
	got, err = s.Search(ctx, "вокзал", nil, "kk", 2)
	if err != nil || len(got) != 2 || got[0].Source != "place" || got[1].Source != "place" {
		t.Fatalf("limit 2 = %+v %v", got, err)
	}
	if n, _ := prov.counts(); n != 2 {
		t.Fatalf("provider called although curated results filled the limit: %d", n)
	}
}

func TestPlacesProviderFailureTolerated(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	addPlace(t, pool, "Орталық базар", "Центральный рынок", "Central bazaar", embiBazaar, "базар", "рынок")
	prov := &fakeGeocoder{err: &providerError{"photon", errors.New("HTTP 502")}}
	s := NewPlaces(pool, PlacesConfig{Provider: prov, ProviderName: "photon", BreakerThreshold: 2,
		BreakerCooldown: time.Hour, CacheTTL: -1, Log: quietLog})

	for i := range 4 {
		got, err := s.Search(ctx, "базар", nil, "kk", 5)
		if err != nil || len(got) != 1 || got[0].Title != "Орталық базар" {
			t.Fatalf("attempt %d = %+v %v", i, got, err)
		}
	}
	// Two failures open the breaker; later calls skip the provider.
	if n, _ := prov.counts(); n != 2 {
		t.Fatalf("provider calls = %d, want 2", n)
	}

	// A slow provider is cut off by the per-call timeout.
	slow := &fakeGeocoder{delay: time.Second, search: []Place{{Title: "x", Point: embi}}}
	s = NewPlaces(pool, PlacesConfig{Provider: slow, ProviderName: "photon", Timeout: 50 * time.Millisecond, CacheTTL: -1, Log: quietLog})
	start := time.Now()
	got, err := s.Search(ctx, "базар", nil, "kk", 5)
	if err != nil || len(got) != 1 || time.Since(start) > 700*time.Millisecond {
		t.Fatalf("slow provider: %+v %v after %v", got, err, time.Since(start))
	}
}

func TestPlacesReverse(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	addPlace(t, pool, "Ембі әкімдігі", "Акимат Эмбы", "Embi akimat", embiAkimat)
	prov := &fakeGeocoder{}
	s := NewPlaces(pool, PlacesConfig{Provider: prov, ProviderName: "photon", Log: quietLog})

	// Nothing found: formatted coordinates and a localized subtitle.
	for lang, sub := range map[string]string{"kk": "Картадағы нүкте", "ru": "Точка на карте", "en": "Point on the map"} {
		r, err := s.Reverse(ctx, embi, lang)
		if err != nil || !r.InServiceArea || r.Title != "48.826400, 58.144200" || r.Subtitle != sub || r.Source != "coordinates" || r.Point != embi {
			t.Fatalf("%s fallback = %+v %v", lang, r, err)
		}
	}
	// Outside Kazakhstan: no provider call, not usable as a stop.
	_, before := prov.counts()
	r, err := s.Reverse(ctx, orenburg, "ru")
	if err != nil || r.InServiceArea || r.Subtitle != "Вне зоны обслуживания" || r.Source != "coordinates" {
		t.Fatalf("outside = %+v %v", r, err)
	}
	if _, after := prov.counts(); after != before {
		t.Fatal("provider called for a point outside Kazakhstan")
	}

	// A curated place within 75 m names the pin; the pin keeps its position.
	pin := geo.Point{Lat: embiAkimat.Lat + 0.0003, Lon: embiAkimat.Lon} // ~33 m
	r, err = s.Reverse(ctx, pin, "kk")
	if err != nil || r.Title != "Ембі әкімдігі" || r.Source != "place" || r.Subtitle != "Ембі" || r.Point != pin.Rounded() {
		t.Fatalf("curated = %+v %v", r, err)
	}

	// Provider address; a foreign result is ignored.
	prov.mu.Lock()
	prov.reverse = &Place{Title: "Жиенқали Тілепбергенов көшесі, 12", Subtitle: "Ембі", Point: embi, Source: "photon", CountryCode: "KZ"}
	prov.mu.Unlock()
	r, err = s.Reverse(ctx, embiBazaar, "kk")
	if err != nil || r.Title != "Жиенқали Тілепбергенов көшесі, 12" || r.Source != "photon" || r.Point != embiBazaar {
		t.Fatalf("provider = %+v %v", r, err)
	}
	prov.mu.Lock()
	prov.reverse = &Place{Title: "Russia", Point: embi, Source: "photon", CountryCode: "RU"}
	prov.mu.Unlock()
	r, err = s.Reverse(ctx, embiPark, "kk")
	if err != nil || r.Source != "coordinates" {
		t.Fatalf("foreign = %+v %v", r, err)
	}
}
