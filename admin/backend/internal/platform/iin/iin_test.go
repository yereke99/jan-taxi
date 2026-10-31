package iin

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	valid := []struct{ in, want string }{
		{"900101300017", "900101300017"},
		{" 9001 0130 0017 ", "900101300017"},
		{"000301500128", "000301500128"}, // leading zeros stay
		{"000102500780", "000102500780"},
		{"860303401029", "860303401029"}, // check digit from the second weights
		{"070707603041", "070707603041"},
		{"991231400892", "991231400892"},
	}
	for _, tc := range valid {
		got, err := Normalize(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
		if !Valid(tc.want) {
			t.Errorf("Valid(%q) = false", tc.want)
		}
	}

	format := []string{
		"", "   ", "90010130001", "9001013000170", "90010130001a", "9001-0130-0017", "９００１０１３０００１７",
		"+90101300017", "90010130001.",
	}
	for _, in := range format {
		if got, err := Normalize(in); !errors.Is(err, ErrFormat) {
			t.Errorf("Normalize(%q) = %q, %v; want ErrFormat", in, got, err)
		}
		if Valid(in) {
			t.Errorf("Valid(%q) = true", in)
		}
	}

	checksum := []string{"900101300018", "000301500129", "860303401020", "111111111111"}
	for _, in := range checksum {
		if got, err := Normalize(in); !errors.Is(err, ErrChecksum) {
			t.Errorf("Normalize(%q) = %q, %v; want ErrChecksum", in, got, err)
		}
		if Valid(in) {
			t.Errorf("Valid(%q) = true", in)
		}
	}
}

func TestMask(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"900101300017", "********0017"},
		{"000301500128", "********0128"},
		{"123", "***"},
		{"12345", "*2345"},
	}
	for _, tc := range tests {
		if got := Mask(tc.in); got != tc.want {
			t.Errorf("Mask(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
