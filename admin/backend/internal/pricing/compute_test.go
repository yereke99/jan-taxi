package pricing

import (
	"slices"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/cities"
)

func TestCompute(t *testing.T) {
	seed := Tariff{MinFare: 500, MaxFare: 1500, FareStep: 50, BaseFare: 500}
	metered := Tariff{MinFare: 500, MaxFare: 5000, FareStep: 50, BaseFare: 300, PerKm: 120, PerMinute: 15, ExtraStopFee: 100}

	tests := []struct {
		name             string
		t                Tariff
		dist, dur, stops int
		wantMin, wantMax int
		wantOptionsLen   int
	}{
		{"seed tariff is flat", seed, 1840, 260, 2, 500, 1500, 21},
		{"seed tariff ignores distance", seed, 250_000, 20_000, 5, 500, 1500, 21},
		// 300 + ceil(120*1.84)=221 + ceil(15*260/60)=65 = 586 -> 600
		{"rounds up to step", metered, 1840, 260, 2, 600, 5000, 89},
		// 300 + ceil(120*1.001)=121 + 0 = 421 -> 450 -> clamped to 500
		{"clamped to min fare", metered, 1001, 0, 2, 500, 5000, 91},
		// 300 + 0 + 0 + 2*100 = 500
		{"extra stops", metered, 0, 0, 4, 500, 5000, 91},
		// 300 + 120*100 = 12300 -> clamped to 5000
		{"clamped to max fare", metered, 100_000, 0, 2, 5000, 5000, 1},
		// 300 + ceil(120*0.001)=1 = 301 -> 350
		{"per km rounds up", Tariff{MinFare: 50, MaxFare: 1000, FareStep: 50, BaseFare: 300, PerKm: 120}, 1, 0, 2, 350, 1000, 14},
		// 0 + ceil(15*1/60)=1 -> 50
		{"per minute rounds up", Tariff{MinFare: 50, MaxFare: 100, FareStep: 50, PerMinute: 15}, 0, 1, 2, 50, 100, 2},
		{"exact multiple stays", Tariff{MinFare: 100, MaxFare: 1000, FareStep: 100, BaseFare: 700}, 0, 0, 2, 700, 1000, 4},
		{"extra fees are added", Tariff{MinFare: 100, MaxFare: 2000, FareStep: 100, BaseFare: 700,
			ExtraFees: []ExtraFee{{Code: "night", Name: cities.Name{KK: "Түн", RU: "Ночь", EN: "Night"}, Amount: 150}}}, 0, 0, 2, 900, 2000, 12},
		{"negative inputs are zero", seed, -5, -5, 1, 500, 1500, 21},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Compute(tt.t, tt.dist, tt.dur, tt.stops)
			if f.Min != tt.wantMin || f.Recommended != tt.wantMin || f.Max != tt.wantMax || f.Step != tt.t.FareStep {
				t.Fatalf("Compute = %+v, want min=recommended=%d max=%d", f, tt.wantMin, tt.wantMax)
			}
			if len(f.Options) != tt.wantOptionsLen || f.Options[0] != f.Min || f.Options[len(f.Options)-1] != f.Max {
				t.Fatalf("options = %v (len %d), want %d from %d to %d", f.Options, len(f.Options), tt.wantOptionsLen, f.Min, f.Max)
			}
			for i := 1; i < len(f.Options); i++ {
				if f.Options[i]-f.Options[i-1] != f.Step || f.Options[i]%f.Step != 0 {
					t.Fatalf("options not on the step grid: %v", f.Options)
				}
			}
		})
	}
}

func TestOptions(t *testing.T) {
	if got := Options(500, 600, 50); !slices.Equal(got, []int{500, 550, 600}) {
		t.Fatalf("Options = %v", got)
	}
	if got := Options(600, 500, 50); len(got) != 0 {
		t.Fatalf("empty range = %v", got)
	}
	if got := Options(500, 600, 0); len(got) != 0 {
		t.Fatalf("zero step = %v", got)
	}
}

func TestCommission(t *testing.T) {
	tests := []struct{ fare, bps, want int }{
		{1000, 1000, 100},
		{555, 1000, 56}, // 55.5 rounds half up
		{545, 1000, 55}, // 54.5 rounds half up
		{544, 1000, 54},
		{500, 0, 0},
		{0, 1000, 0},
		{1500, 5000, 750},
		{999, 1, 0},  // 0.0999
		{5000, 1, 1}, // 0.5 rounds up
		{2_000_000_000, 5000, 1_000_000_000},
	}
	for _, tt := range tests {
		if got := Commission(tt.fare, tt.bps); got != tt.want {
			t.Errorf("Commission(%d, %d) = %d, want %d", tt.fare, tt.bps, got, tt.want)
		}
	}
}

func TestValidateFare(t *testing.T) {
	q := StoredQuote{MinFare: 600, MaxFare: 1500, FareStep: 50, CommissionBps: 1000}
	for _, fare := range []int{600, 650, 1500} {
		if err := q.ValidateFare(fare); err != nil {
			t.Errorf("fare %d rejected: %v", fare, err)
		}
	}
	for _, fare := range []int{0, 550, 610, 1550, -50} {
		he := httpxError(t, q.ValidateFare(fare))
		if he.Status != 422 || he.Code != "fare_not_allowed" || he.Details["min"] != 600 || he.Details["max"] != 1500 || he.Details["step"] != 50 {
			t.Fatalf("fare %d: %v", fare, he)
		}
	}
	if got := q.Commission(650); got != 65 {
		t.Fatalf("Commission = %d", got)
	}
	if fo := q.FareOptions(); len(fo.Options) != 19 || fo.Options[0] != 600 {
		t.Fatalf("FareOptions = %+v", fo)
	}
}

func TestStopsHash(t *testing.T) {
	a := []QuoteStop{{Lat: 48.8264, Lon: 58.1442, Address: "A"}, {Lat: 48.8291, Lon: 58.1504, Address: "B", AddressDetails: "2"}}
	h := StopsHash(a)
	if len(h) != 64 || h != StopsHash(slices.Clone(a)) {
		t.Fatalf("hash not deterministic: %s", h)
	}
	// Sub-micro-degree noise does not change the hash (6-decimal rounding).
	noisy := slices.Clone(a)
	noisy[0].Lat += 1e-8
	if StopsHash(noisy) != h {
		t.Fatal("rounding to 6 decimals not applied")
	}
	for name, mut := range map[string]func([]QuoteStop){
		"order":   func(s []QuoteStop) { s[0], s[1] = s[1], s[0] },
		"address": func(s []QuoteStop) { s[1].Address = "C" },
		"details": func(s []QuoteStop) { s[1].AddressDetails = "" },
		"coord":   func(s []QuoteStop) { s[1].Lon += 0.000001 },
	} {
		c := slices.Clone(a)
		mut(c)
		if StopsHash(c) == h {
			t.Errorf("%s change did not change the hash", name)
		}
	}
}
