package phone

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		in, want string
		err      error
	}{
		{"8 701 234 56 78", "+77012345678", nil},
		{"+7 701 234 56 78", "+77012345678", nil},
		{"+7 (701) 234-56-78", "+77012345678", nil},
		{"87012345678", "+77012345678", nil},
		{"77012345678", "+77012345678", nil},
		{"7012345678", "+77012345678", nil},
		{"701 234 56 78", "+77012345678", nil},
		{"8.747.123.45.67", "+77471234567", nil},
		{"  +7 778 000 00 00 ", "+77780000000", nil},
		{"8 700 000 00 01", "+77000000001", nil},
		{"8 702 111 22 33", "+77021112233", nil},
		{"8 705 111 22 33", "+77051112233", nil},
		{"8 708 111 22 33", "+77081112233", nil},
		{"8 760 111 22 33", "+77601112233", nil},
		{"8 764 111 22 33", "+77641112233", nil},
		{"8 771 111 22 33", "+77711112233", nil},
		{"8 775 111 22 33", "+77751112233", nil},
		{"8 776 111 22 33", "+77761112233", nil},
		{"8 777 111 22 33", "+77771112233", nil},
		{"8 785 111 22 33", "+77851112233", nil},

		// Not mobile: Russian mobile, KZ landline (Aktobe 7132), VoIP 751, unused ranges.
		{"+7 916 123 45 67", "", ErrNotMobile},
		{"8 7132 12 34 56", "", ErrNotMobile},
		{"8 751 111 22 33", "", ErrNotMobile},
		{"8 703 111 22 33", "", ErrNotMobile},
		{"8 750 111 22 33", "", ErrNotMobile},
		{"8 765 111 22 33", "", ErrNotMobile},
		{"8 779 111 22 33", "", ErrNotMobile},

		// Malformed.
		{"", "", ErrInvalid},
		{"+8 701 234 56 78", "", ErrInvalid},
		{"+77012345678 ext 5", "", ErrInvalid},
		{"7012+345678", "", ErrInvalid},
		{"870123456789", "", ErrInvalid},
		{"801234567", "", ErrInvalid},
		{"+1 701 234 5678", "", ErrInvalid},
		{"８７０１２３４５６７８", "", ErrInvalid},
		{"tel:+77012345678", "", ErrInvalid},
	}
	for _, tc := range tests {
		got, err := Normalize(tc.in)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("Normalize(%q) = %q, %v; want %q, %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestFormatMaskValid(t *testing.T) {
	if got := Format("+77012345678"); got != "+7 701 234 56 78" {
		t.Errorf("Format = %q", got)
	}
	if got := Format("bad"); got != "bad" {
		t.Errorf("Format(bad) = %q", got)
	}
	if got := Mask("8 (701) 234-56-78"); got != "+7 701 *** ** 78" {
		t.Errorf("Mask = %q", got)
	}
	if got := Mask("+79161234567"); got != "" {
		t.Errorf("Mask(non-KZ) = %q", got)
	}
	if !Valid("+77012345678") || Valid("77012345678") || Valid("+79161234567") || Valid("+77512345678") {
		t.Error("Valid")
	}
}
