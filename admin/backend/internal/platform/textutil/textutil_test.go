package textutil

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClean(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"trim and collapse", "  Сәлем   әлем  ", "Сәлем әлем"},
		{"newlines and tabs", "бірінші\nекінші\r\nүшінші\tтөртінші", "бірінші екінші үшінші төртінші"},
		{"unicode spaces", "a\u00a0\u2003b\u3000c\u2028d", "a b c d"},
		{"control chars", "a\x00b\x07c\x1bd\x7fe\u0085f", "abcde f"},
		{"bidi overrides", "abc\u202edcba\u202c ok \u2066x\u2069 \u200fy", "abcdcba ok x y"},
		{"zero width and bom", "\ufeffa\u200bb\u2060c\u00add", "abcd"},
		{"keeps emoji zwj", "👨\u200d👩\u200d👧 family", "👨\u200d👩\u200d👧 family"},
		{"keeps variation selector", "❤\ufe0f", "❤\ufe0f"},
		{"nfc combining breve", "и\u0306", "й"},
		{"nfc latin acute", "e\u0301", "é"},
		{"nfc after removal", "e\u200b\u0301", "é"},
		{"invalid utf8 dropped", "ab\xffcd", "abcd"},
		{"only spaces", " \t\n ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clean(tc.in); got != tc.want {
				t.Fatalf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanTextLimit(t *testing.T) {
	// 10 Kazakh letters with diacritics repeated 12 times = 120 code points.
	unit := "әғқңөұүһіа"
	if utf8.RuneCountInString(unit) != 10 {
		t.Fatal("unit must be 10 code points")
	}
	ok120 := strings.Repeat(unit, 12)
	got, err := CleanText(ok120, MaxFreeText)
	if err != nil || got != ok120 {
		t.Fatalf("120 code points must pass: %v", err)
	}
	if _, err := CleanText(ok120+"ә", MaxFreeText); !errors.Is(err, ErrTooLong) {
		t.Fatalf("121 code points must fail with ErrTooLong, got %v", err)
	}

	// Decomposed input counts after NFC: 120 × (и + U+0306) is 240 code points
	// raw but 120 after normalisation.
	decomposed := strings.Repeat("и\u0306", 120)
	if got, err := CleanText(decomposed, 120); err != nil || utf8.RuneCountInString(got) != 120 {
		t.Fatalf("decomposed input must be measured after NFC: %d %v", utf8.RuneCountInString(got), err)
	}
	if Len(decomposed) != 120 {
		t.Fatalf("Len = %d, want 120", Len(decomposed))
	}

	// Whitespace collapsing happens before measuring.
	padded := "  " + strings.Repeat("ab  ", 30) // 30×"ab" + 29 spaces = 89
	if got, err := CleanText(padded, 89); err != nil || utf8.RuneCountInString(got) != 89 {
		t.Fatalf("padded: %q %v", got, err)
	}

	// Emoji are counted as code points, like the Mini Apps do.
	if Len("🚕") != 1 || Len("👨\u200d👩\u200d👧") != 5 {
		t.Fatalf("emoji counting: %d %d", Len("🚕"), Len("👨\u200d👩\u200d👧"))
	}
	if _, err := CleanText(strings.Repeat("x", 500), 0); err != nil {
		t.Fatal("max <= 0 disables the limit")
	}
}

func TestCleanRequiredAndOptional(t *testing.T) {
	if _, err := CleanRequired(" \u200b\n ", 120); !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
	if got, err := CleanRequired(" Құжаттар ", 120); err != nil || got != "Құжаттар" {
		t.Fatalf("got %q %v", got, err)
	}
	if v, err := CleanOptional(nil, 120); v != nil || err != nil {
		t.Fatal("nil in, nil out")
	}
	blank := "   "
	if v, err := CleanOptional(&blank, 120); v != nil || err != nil {
		t.Fatal("blank optional must become nil")
	}
	long := strings.Repeat("a", 121)
	if _, err := CleanOptional(&long, 120); !errors.Is(err, ErrTooLong) {
		t.Fatal("optional too long must fail")
	}
}

func TestReason(t *testing.T) {
	if Reason(ErrTooLong) != "too_long" || Reason(ErrEmpty) != "required" || Reason(errors.New("x")) != "invalid" {
		t.Fatal("reason mapping")
	}
}
