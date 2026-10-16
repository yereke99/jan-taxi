package cities

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/geo"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

func TestFold(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Ембі  ", "емби"},
		{"ҚАЗАҚСТАН", "казакстан"},
		{"Әкімдік", "акимдик"},
		{"Ұлы-Дала, №5", "улы дала 5"},
		{"Ёлка", "елка"},
		{"Bus\tStation", "bus station"},
		{"Ё", "е"}, // decomposed ё composes, then folds
		{"!!!", ""},
	}
	for _, tt := range tests {
		if got := Fold(tt.in); got != tt.want {
			t.Errorf("Fold(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMatchRank(t *testing.T) {
	tests := []struct {
		hay, needle string
		want        int
	}{
		{"автовокзал ембі", "авто", 0},
		{"орталық автовокзал", "авто", 1},
		{"ембі автовокзалы", "вокзал", 2},
		{"базар", "вокзал", -1},
		{"", "a", -1},
	}
	for _, tt := range tests {
		if got := matchRank(tt.hay, tt.needle); got != tt.want {
			t.Errorf("matchRank(%q, %q) = %d, want %d", tt.hay, tt.needle, got, tt.want)
		}
	}
}

func insertPlace(t *testing.T, pool *pgxpool.Pool, kk, ru, en string, aliases []string, p geo.Point, active bool) {
	t.Helper()
	if aliases == nil {
		aliases = []string{}
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO places (name_kk, name_ru, name_en, aliases, lat, lon, is_active) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		kk, ru, en, aliases, p.Lat, p.Lon, active); err != nil {
		t.Fatal(err)
	}
}

func TestSearchPlaces(t *testing.T) {
	pool := testutil.NewDB(t)
	ctx := context.Background()
	insertPlace(t, pool, "Ембі автовокзалы", "Автовокзал Эмба", "Embi bus station", []string{"вокзал"}, geo.Point{Lat: 48.83, Lon: 58.15}, true)
	insertPlace(t, pool, "Орталық базар", "Центральный рынок", "Central bazaar", []string{"базар", "рынок"}, geo.Point{Lat: 48.82, Lon: 58.14}, true)
	insertPlace(t, pool, "Әкімдік", "Акимат", "Akimat", nil, geo.Point{Lat: 48.826, Lon: 58.144}, true)
	insertPlace(t, pool, "Ақтөбе әкімдігі", "Акимат Актобе", "Aktobe akimat", nil, geo.Point{Lat: 50.28, Lon: 57.17}, true)
	insertPlace(t, pool, "Жабық базар", "Закрытый рынок", "Closed market", nil, geo.Point{Lat: 48.82, Lon: 58.14}, false)

	titles := func(ps []Place, lang string) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.Title(lang)
		}
		return out
	}
	near := geo.Point{Lat: 50.28, Lon: 57.17} // Aktobe
	tests := []struct {
		name  string
		query string
		near  *geo.Point
		lang  string
		limit int
		want  []string
	}{
		{"russian keyboard for kazakh letters, prefix before word prefix", "акимд", nil, "kk", 10, []string{"Әкімдік", "Ақтөбе әкімдігі"}},
		{"equal rank ordered by name", "акимат", nil, "ru", 10, []string{"Акимат", "Акимат Актобе"}},
		{"prefix ties broken by distance", "акимат", &near, "ru", 10, []string{"Акимат Актобе", "Акимат"}},
		{"alias and word prefix", "вокзал", nil, "en", 10, []string{"Embi bus station"}},
		{"english case-insensitive", "CENTRAL", nil, "en", 10, []string{"Central bazaar"}},
		{"inactive hidden", "закрыт", nil, "ru", 10, []string{}},
		{"limit", "а", nil, "kk", 1, []string{"Әкімдік"}},
		{"blank query", "   ", nil, "kk", 10, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SearchPlaces(ctx, pool, tt.query, tt.near, tt.lang, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			g := titles(got, tt.lang)
			if len(g) != len(tt.want) {
				t.Fatalf("got %v, want %v", g, tt.want)
			}
			for i := range g {
				if g[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", g, tt.want)
				}
			}
		})
	}
}
