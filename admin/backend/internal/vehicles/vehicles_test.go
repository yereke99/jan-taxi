package vehicles

import "testing"

func TestPlateDisplayAndKind(t *testing.T) {
	cases := []struct {
		v             Vehicle
		display, kind string
	}{
		{Vehicle{PlateRaw: "123 авс 04", PlateNormalized: "123ABC04"}, "123 ABC 04", "current"},
		{Vehicle{PlateRaw: "d123abc", PlateNormalized: "D123ABC"}, "D 123 ABC", "legacy"},
		{Vehicle{PlateRaw: "12"}, "12", ""},
		{Vehicle{}, "", ""},
	}
	for _, tt := range cases {
		if got := tt.v.PlateDisplay(); got != tt.display {
			t.Errorf("PlateDisplay(%q) = %q, want %q", tt.v.PlateRaw, got, tt.display)
		}
		if got := tt.v.PlateKind(); got != tt.kind {
			t.Errorf("PlateKind(%q) = %q, want %q", tt.v.PlateNormalized, got, tt.kind)
		}
	}
}
