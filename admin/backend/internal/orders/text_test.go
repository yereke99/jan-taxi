package orders

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestContainsBlocked(t *testing.T) {
	keywords := []string{"қару", "оружие", "weapon", "есірткі", "наркот", "drug"}
	cases := []struct {
		text string
		want bool
	}{
		{"Құжаттар салынған конверт", false},
		{"ҚАРУ", true},
		{"Аңшылық қаруы", true},       // inflected Kazakh form
		{"оружием и патронами", true}, // inflected Russian form
		{"Drugs", true},
		{"Наркотики", true},
		{"есірткі", true},
		{"Weapon-shaped toy", true},
		{"Ойыншықтар және кітаптар", false},
		{"bedrug cover", false}, // keyword inside a word, not at its start
		{"сақару", false},       // ditto
		{"е́сірткі", false},     // a combining mark breaks the keyword
		{"", false},
	}
	for _, c := range cases {
		if got := containsBlocked(c.text, keywords); got != c.want {
			t.Errorf("containsBlocked(%q) = %v, want %v", c.text, got, c.want)
		}
	}
	// Keywords are normalised too.
	if !containsBlocked("Қару", []string{"  ҚАРУ "}) {
		t.Error("keyword case/space not normalised")
	}
	if containsBlocked("anything", []string{"", "  "}) {
		t.Error("blank keyword matched")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 16, 9, 30, 0, 123456789, time.UTC)
	id := uuid.New()
	c, err := decodeCursor(*encodeCursor(at, id))
	if err != nil || c == nil || !c.CreatedAt.Equal(at) || c.ID != id {
		t.Fatalf("round trip: %+v %v", c, err)
	}
	if c, err := decodeCursor(""); c != nil || err != nil {
		t.Fatalf("empty cursor: %+v %v", c, err)
	}
	for _, bad := range []string{"%%%", "bm90LWEtY3Vyc29y", "MjAyNi0xMC0xNlQwOTozMDowMFp8bm9wZQ", string(make([]byte, 201))} {
		if _, err := decodeCursor(bad); err == nil {
			t.Errorf("decodeCursor(%q) accepted", bad)
		}
	}
}

func TestAverageRatingAndInitial(t *testing.T) {
	if averageRating(0, 0) != nil {
		t.Error("rating without votes")
	}
	if r := averageRating(14, 3); r == nil || *r != 4.7 {
		t.Errorf("average = %v", r)
	}
	if got := initial("Бекенов"); got != "Б." {
		t.Errorf("initial = %q", got)
	}
	if got := initial(""); got != "" {
		t.Errorf("empty initial = %q", got)
	}
}
