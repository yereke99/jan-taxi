package reports

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParsePeriod(t *testing.T) {
	loc := loadLocation(DefaultTimezone)
	now := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC) // 08:00 in Aqtobe
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, loc) }
	cases := []struct {
		query    string
		from, to time.Time
		field    string
	}{
		{"", day(2026, 10, 16), day(2026, 10, 17), ""},
		{"from=2026-10-01&to=2026-10-03", day(2026, 10, 1), day(2026, 10, 4), ""},
		{"from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), ""},
		{"from=2026-10-10", day(2026, 10, 10), day(2026, 10, 17), ""},
		{"to=2026-10-05", day(2026, 10, 5), day(2026, 10, 6), ""},
		{"from=2026-10-05&to=2026-10-04", time.Time{}, time.Time{}, "to"},
		{"from=yesterday", time.Time{}, time.Time{}, "from"},
		{"from=2026-01-01&to=2026-10-01", time.Time{}, time.Time{}, "to"},
		{"from=2026-07-02&to=2026-10-01", day(2026, 7, 2), day(2026, 10, 2), ""}, // exactly 92 days
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/dashboard?"+tc.query, nil)
		p, err := parsePeriod(r, loc, now, 1, MaxDashboardDays)
		if tc.field != "" {
			if err == nil || !strings.Contains(err.Error(), "validation_failed") {
				t.Fatalf("%q: err = %v", tc.query, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.query, err)
		}
		if !p.From.Equal(tc.from) || !p.To.Equal(tc.to) {
			t.Fatalf("%q: [%s, %s), want [%s, %s)", tc.query, p.From, p.To, tc.from, tc.to)
		}
	}
	// Export default: the 30 local days ending today.
	p, err := parsePeriod(httptest.NewRequest("GET", "/x", nil), loc, now, 30, MaxExportDays)
	if err != nil || !p.From.Equal(day(2026, 9, 17)) || !p.To.Equal(day(2026, 10, 17)) {
		t.Fatalf("export default = %v, %v", p, err)
	}
	if got := (Period{From: day(2026, 10, 1), To: day(2026, 10, 3).Add(time.Hour), Loc: loc}).Days(); strings.Join(got, ",") != "2026-10-01,2026-10-02,2026-10-03" {
		t.Fatalf("days = %v", got)
	}
}

func TestEscapeFormula(t *testing.T) {
	cases := map[string]string{
		"=HYPERLINK(\"http://x\")": "'=HYPERLINK(\"http://x\")",
		"+7 701 123 45 67":         "'+7 701 123 45 67",
		"-2+3":                     "'-2+3",
		"@SUM(A1)":                 "'@SUM(A1)",
		"\tcmd":                    "'\tcmd",
		"\rcmd":                    "'\rcmd",
		"Ембі, Абай көшесі 5":      "Ембі, Абай көшесі 5",
		"a=b":                      "a=b",
		"":                         "",
	}
	for in, want := range cases {
		if got := EscapeFormula(in); got != want {
			t.Fatalf("EscapeFormula(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCSVWriter(t *testing.T) {
	var buf bytes.Buffer
	cw, err := NewCSVWriter(&buf, "id", "text", "amount", "at")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 5, 0, 0, 0, time.UTC)
	if err := cw.Row(Int(1), Text(`=1+1, "quoted"`), Int(-80), Time(&at, loadLocation(DefaultTimezone))); err != nil {
		t.Fatal(err)
	}
	if err := cw.Row(Int(2), TextPtr(nil), IntPtr(nil), Time(nil, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}
	want := utf8BOM + "id,text,amount,at\r\n" +
		`1,"'=1+1, ""quoted""",-80,2026-10-01T10:00:00+05:00` + "\r\n" +
		"2,,,\r\n"
	if buf.String() != want {
		t.Fatalf("csv =\n%q\nwant\n%q", buf.String(), want)
	}
	if cw.Rows() != 2 {
		t.Fatalf("rows = %d", cw.Rows())
	}
}
