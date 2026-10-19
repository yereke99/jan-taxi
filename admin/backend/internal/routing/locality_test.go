package routing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
)

// setGeo changes the scripted geocoder between requests.
func (e *testEnv) setGeo(fn func(g *fakeGeocoder)) {
	e.geo.mu.Lock()
	defer e.geo.mu.Unlock()
	fn(e.geo)
}

func TestPreviewLocality(t *testing.T) {
	env := newTestEnv(t, nil)
	token, userID := env.signIn("passenger")
	ctx := context.Background()
	almaty := geo.Point{Lat: 43.2389, Lon: 76.8897}
	almatyDostyk := geo.Point{Lat: 43.2567, Lon: 76.9286}
	env.setGeo(func(g *fakeGeocoder) {
		g.reverse = &Place{Title: "Абай даңғылы, 10", Point: almaty, Source: "photon", CountryCode: "KZ", Locality: " Алматы "}
	})

	// Almaty has no city zone: the nationwide region prices it and the
	// geocoder names the locality, looked up once for all class options.
	_, reversesBefore := env.geo.counts()
	status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token,
		previewBody("taxi", stopJSON(almaty, "Абай даңғылы, 10"), stopJSON(almatyDostyk, "Достық даңғылы, 50")))
	if status != http.StatusOK {
		t.Fatalf("almaty preview: %d %v", status, out)
	}
	region, _ := out["tariff_region"].(map[string]any)
	name, _ := region["name"].(map[string]any)
	if out["locality"] != "Алматы" || region["code"] != "KAZ" || name["kk"] != "Қазақстан" || name["ru"] != "Казахстан" ||
		name["en"] != "Kazakhstan" || out["city"].(map[string]any)["code"] != "KAZ" {
		t.Fatalf("almaty preview: locality %v, region %v, city %v", out["locality"], region, out["city"])
	}
	if _, reverses := env.geo.counts(); reverses-reversesBefore != 1 {
		t.Errorf("reverse lookups per preview = %d, want 1", reverses-reversesBefore)
	}
	options, _ := out["options"].([]any)
	var quotes, named int
	if err := env.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE locality = 'Алматы')
		FROM fare_quotes WHERE user_id = $1`, userID).Scan(&quotes, &named); err != nil {
		t.Fatal(err)
	}
	if len(options) < 2 || quotes != len(options) || named != quotes {
		t.Fatalf("quotes = %d (named %d), options = %d", quotes, named, len(options))
	}

	// Inside Embi's zone the city tariff applies.
	env.setGeo(func(g *fakeGeocoder) {
		g.reverse = &Place{Title: "Вокзал көшесі, 3", Point: embi, Source: "photon", CountryCode: "KZ", Locality: "Ембі"}
	})
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, previewBody("taxi", stopJSON(embi, "A"), stopJSON(embiStation, "B")))
	if status != http.StatusOK || out["locality"] != "Ембі" || out["tariff_region"].(map[string]any)["code"] != "EMB" {
		t.Fatalf("embi preview: %d %v %v", status, out["locality"], out["tariff_region"])
	}

	// No usable answer: null, never coordinates; the preview still works.
	cases := []struct {
		name string
		set  func(g *fakeGeocoder)
	}{
		{"provider error", func(g *fakeGeocoder) { g.reverse, g.err = nil, errors.New("photon down") }},
		{"no place", func(g *fakeGeocoder) { g.reverse, g.err = nil, nil }},
		{"no locality", func(g *fakeGeocoder) {
			g.reverse = &Place{Title: "Дала", Point: almaty, Source: "photon", CountryCode: "KZ"}
		}},
		{"coordinates", func(g *fakeGeocoder) {
			g.reverse = &Place{Title: "43.2389, 76.8897", Point: almaty, Source: "photon", CountryCode: "KZ", Locality: "43.2389, 76.8897"}
		}},
	}
	for _, tc := range cases {
		env.setGeo(tc.set)
		status, out := env.do(http.MethodPost, "/api/v1/routes/preview", token, previewBody("taxi", stopJSON(almaty, "A"), stopJSON(almatyDostyk, "B")))
		if status != http.StatusOK || out["locality"] != nil || out["tariff_region"].(map[string]any)["code"] != "KAZ" {
			t.Errorf("%s: %d locality %v", tc.name, status, out["locality"])
		}
	}

	// A slow geocoder is cut off after LocalityTimeout.
	env.setGeo(func(g *fakeGeocoder) {
		g.err, g.reverseDelay = nil, 10*time.Second
		g.reverse = &Place{Title: "Абай даңғылы, 10", Point: almaty, Source: "photon", CountryCode: "KZ", Locality: "Алматы"}
	})
	start := time.Now()
	status, out = env.do(http.MethodPost, "/api/v1/routes/preview", token, previewBody("taxi", stopJSON(almaty, "A"), stopJSON(almatyDostyk, "B")))
	if took := time.Since(start); status != http.StatusOK || out["locality"] != nil || took > LocalityTimeout+time.Second {
		t.Errorf("slow geocoder: %d locality %v after %s", status, out["locality"], took)
	}
}

func TestCleanLocality(t *testing.T) {
	long := strings.Repeat("Ә", 130)
	tests := []struct {
		in   string
		want string // "" means nil
	}{
		{"Алматы", "Алматы"},
		{"  Ақтөбе \n", "Ақтөбе"},
		{"Мұғалжар ауданы", "Мұғалжар ауданы"},
		{"", ""},
		{"   ", ""},
		{"43.2389, 76.8897", ""},
		{"43.2389 76.8897", ""},
		{"43.2° N 76.9° E", ""},
		{"-12.5;+40", ""},
		{"Ауыл 5", "Ауыл 5"},
		{long, strings.Repeat("Ә", 120)},
	}
	for _, tt := range tests {
		got := CleanLocality(tt.in)
		switch {
		case tt.want == "" && got != nil:
			t.Errorf("CleanLocality(%q) = %q, want nil", tt.in, *got)
		case tt.want != "" && (got == nil || *got != tt.want):
			t.Errorf("CleanLocality(%q) = %v, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPhotonLocality(t *testing.T) {
	tests := []struct {
		p    photonProps
		want string
	}{
		{photonProps{Name: "Ембі", OsmKey: "place", OsmValue: "town", County: "Мұғалжар ауданы"}, "Ембі"},
		{photonProps{Name: "Шұбарқұдық", OsmKey: "place", OsmValue: "village"}, "Шұбарқұдық"},
		{photonProps{Name: "Абай", OsmKey: "highway", OsmValue: "primary", City: "Алматы", District: "Медеу ауданы"}, "Алматы"},
		{photonProps{Street: "Тәуелсіздік", County: "Мұғалжар ауданы", District: "Шет"}, "Мұғалжар ауданы"},
		{photonProps{District: "Алмалы ауданы"}, "Алмалы ауданы"},
		{photonProps{Name: "Қазақстан", OsmKey: "place", OsmValue: "country"}, ""},
	}
	for _, tt := range tests {
		if got := photonLocality(tt.p); got != tt.want {
			t.Errorf("photonLocality(%+v) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

// settlementGeocoder is a fakeGeocoder that also names settlements.
type settlementGeocoder struct {
	*fakeGeocoder
	mu          sync.Mutex
	name        string
	err         error
	settlements int
}

func (g *settlementGeocoder) Settlement(context.Context, geo.Point, string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.settlements++
	return g.name, g.err
}

func TestLocalityPrefersTheCoveringSettlement(t *testing.T) {
	embiAddress := &Place{Title: "Жети казына", Point: embi, Source: "photon", CountryCode: "KZ", Locality: "Эмба"}
	tests := []struct {
		name       string
		settlement string
		err        error
		want       string // "" means nil
	}{
		{"settlement wins over the address form", "Ембі", nil, "Ембі"},
		{"no settlement covers the point", "", nil, "Эмба"},
		{"settlement lookup failed", "", errors.New("photon down"), "Эмба"},
		{"settlement looks like coordinates", "48.8264, 58.1442", nil, "Эмба"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := &settlementGeocoder{fakeGeocoder: &fakeGeocoder{reverse: embiAddress}, name: tt.settlement, err: tt.err}
			s := NewPlaces(nil, PlacesConfig{Provider: prov, ProviderName: "photon", Log: quietLog})
			got := s.Locality(context.Background(), embi)
			switch {
			case tt.want == "" && got != nil:
				t.Fatalf("locality = %q, want nil", *got)
			case tt.want != "" && (got == nil || *got != tt.want):
				t.Fatalf("locality = %v, want %q", got, tt.want)
			}
			// The settlement answer is cached: a second preview at the same
			// pickup gets the same locality without asking again.
			again := s.Locality(context.Background(), embi)
			prov.mu.Lock()
			asked := prov.settlements
			prov.mu.Unlock()
			if (again == nil) != (got == nil) || (again != nil && *again != *got) || (tt.err == nil && asked != 1) {
				t.Fatalf("second lookup: %v, settlement lookups %d", again, asked)
			}
		})
	}
}
