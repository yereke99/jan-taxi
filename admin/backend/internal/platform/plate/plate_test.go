package plate

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		kind   Kind
		region string
	}{
		{"cyrillic look-alikes", "123 АВС 04", "123ABC04", KindCurrent, "04"},
		{"latin with spaces", "123 ABC 04", "123ABC04", KindCurrent, "04"},
		{"lowercase and dashes", "123-abc-04", "123ABC04", KindCurrent, "04"},
		{"lowercase cyrillic", "777 кхм 02", "777KXM02", KindCurrent, "02"},
		{"two letters legal entity", "001 AB 01", "001AB01", KindCurrent, "01"},
		{"region 20 ulytau", "555 ННН 20", "555HHH20", KindCurrent, "20"},
		{"kazakh u", "123 ҮВС 13", "123YBC13", KindCurrent, "13"},
		{"kz prefix", "KZ 123 ABC 04", "123ABC04", KindCurrent, "04"},
		{"cyrillic kz prefix", "КZ 123 ABC 04", "123ABC04", KindCurrent, "04"},
		{"full-width", "１２３ ＡＢＣ ０４", "123ABC04", KindCurrent, "04"},
		{"unicode dash", "123–ABC–04", "123ABC04", KindCurrent, "04"},
		{"legacy aktobe", "D 123 ABC", "D123ABC", KindLegacy, "D"},
		{"legacy two letters", "d123ac", "D123AC", KindLegacy, "D"},
		{"legacy cyrillic", "Е 456 КМ", "E456KM", KindLegacy, "E"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if p.Normalized != tc.want || p.Kind != tc.kind || p.Region != tc.region {
				t.Fatalf("Parse(%q) = %+v; want %s %s %s", tc.in, p, tc.want, tc.kind, tc.region)
			}
			n, err := Normalize(tc.in)
			if err != nil || n != tc.want {
				t.Fatalf("Normalize(%q) = %q, %v", tc.in, n, err)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"123 ABC 00", // region 00 does not exist
		"123 ABC 21", // beyond Ulytau (20)
		"123 ABC 99",
		"12 ABC 04",
		"1234 ABC 04",
		"123 A 04",
		"123 ABCD 04",
		"123 ДЖЗ 04", // Cyrillic letters without a Latin look-alike
		"123 ABC 04 extra",
		"А123ВС77RUS",
		"D 12 ABC",
		"123ABC04!",
		"KZ",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	} {
		if p, err := Parse(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %+v, %v; want ErrInvalid", in, p, err)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := []struct{ in, want string }{
		{"123ABC04", "123 ABC 04"},
		{"001AB01", "001 AB 01"},
		{"D123ABC", "D 123 ABC"},
		{"garbage", "garbage"},
	}
	for _, tc := range tests {
		if got := Format(tc.in); got != tc.want {
			t.Errorf("Format(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizedFitsSchema(t *testing.T) {
	// vehicles.plate_normalized CHECK: ^[0-9A-Z]{5,10}$
	for _, in := range []string{"001AB01", "123ABC04", "D123AC", "D123ABC"} {
		n, err := Normalize(in)
		if err != nil || len(n) < 5 || len(n) > 10 {
			t.Errorf("%q -> %q (%v) does not fit the schema check", in, n, err)
		}
	}
}
