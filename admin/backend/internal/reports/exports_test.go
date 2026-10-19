package reports

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
)

// readCSV checks the BOM and CRLF framing and returns the records.
func readCSV(t *testing.T, body string) [][]string {
	t.Helper()
	if !strings.HasPrefix(body, utf8BOM) {
		t.Fatalf("no BOM: %q", body[:min(len(body), 20)])
	}
	body = strings.TrimPrefix(body, utf8BOM)
	if !strings.HasSuffix(body, "\r\n") || strings.Contains(strings.ReplaceAll(body, "\r\n", ""), "\n") {
		t.Fatalf("not CRLF framed: %q", body)
	}
	recs, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return recs
}

func column(t *testing.T, recs [][]string, name string) int {
	t.Helper()
	for i, h := range recs[0] {
		if h == name {
			return i
		}
	}
	t.Fatalf("no column %s in %v", name, recs[0])
	return -1
}

func TestExports(t *testing.T) {
	f := newFixture(t)
	h := adminHandler(t, f.pool, auth.RoleFinanceManager)
	from := f.base.Format(time.DateOnly)
	to := f.base.AddDate(0, 0, 2).Format(time.DateOnly)
	period := "from=" + from + "&to=" + to

	// Orders: one row per order created in the period; user text is defused.
	rec := get(h, "/reports/orders.csv?"+period)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("orders.csv: %d %v", rec.Code, rec.Header())
	}
	wantName := `attachment; filename="orders-` + from + `-` + f.base.AddDate(0, 0, 3).Format(time.DateOnly) + `.csv"`
	if rec.Header().Get("Content-Disposition") != wantName {
		t.Fatalf("disposition = %q, want %q", rec.Header().Get("Content-Disposition"), wantName)
	}
	recs := readCSV(t, rec.Body.String())
	if len(recs) != 7 {
		t.Fatalf("orders rows = %d", len(recs)-1)
	}
	pick, dest, reason := column(t, recs, "pickup_address"), column(t, recs, "destination_address"), column(t, recs, "cancel_reason")
	if city, locality := column(t, recs, "city"), column(t, recs, "locality"); locality != city+1 || recs[1][locality] != "" {
		t.Fatalf("locality column %d after city %d: %v", locality, city, recs[1])
	}
	var sawLink, sawReason bool
	for _, r := range recs[1:] {
		if r[pick] == `'=HYPERLINK("http://evil.example","click")` && r[dest] == "Ембі, Абай көшесі 5" {
			sawLink = true
		}
		if r[reason] == "'@SUM(1)" {
			sawReason = true
		}
		if strings.HasPrefix(r[pick], "=") || strings.HasPrefix(r[reason], "@") {
			t.Fatalf("formula leaked: %v", r)
		}
	}
	if !sawLink || !sawReason {
		t.Fatalf("escaped cells missing: %v", recs)
	}
	if !strings.Contains(rec.Body.String(), `"'=HYPERLINK(""http://evil.example"",""click"")"`) {
		t.Fatal("quotes not doubled per RFC 4180")
	}
	if rec := get(h, "/reports/orders.csv?"+period+"&status=cancelled&city_id="+f.cityID.String()); len(readCSV(t, rec.Body.String())) != 3 {
		t.Fatalf("filtered orders = %s", rec.Body)
	}

	// Ledger: negative amounts stay numeric; descriptions are defused.
	rec = get(h, "/reports/ledger.csv?"+period)
	recs = readCSV(t, rec.Body.String())
	if len(recs) != 6 {
		t.Fatalf("ledger rows = %d", len(recs)-1)
	}
	delta, desc := column(t, recs, "balance_delta"), column(t, recs, "description")
	if recs[1][delta] != "-100" && recs[1][delta] != "-80" || recs[1][desc] != "'@SUM(1)" {
		t.Fatalf("ledger row = %v", recs[1])
	}
	rec = get(h, "/reports/ledger.csv?"+period+"&type=commission_refund&driver_id="+f.d2.DriverID.String())
	if recs := readCSV(t, rec.Body.String()); len(recs) != 2 || recs[1][delta] != "150" {
		t.Fatalf("filtered ledger = %v", recs)
	}

	// Top-ups.
	rec = get(h, "/reports/topups.csv?"+period+"&status=confirmed")
	recs = readCSV(t, rec.Body.String())
	if len(recs) != 2 || recs[1][column(t, recs, "driver_payment_reference")] != "'+cmd" ||
		recs[1][column(t, recs, "verified_amount")] != "2000" || recs[1][column(t, recs, "confirmed_by")] != "fixture@jantaxi.test" {
		t.Fatalf("topups = %v", recs)
	}
	if recs := readCSV(t, get(h, "/reports/topups.csv?"+period).Body.String()); len(recs) != 4 {
		t.Fatalf("all top-ups = %d", len(recs)-1)
	}

	// Every export is audited.
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_audit_logs WHERE action = 'report.export'`).Scan(&n); err != nil || n != 6 {
		t.Fatalf("audit rows = %d, %v", n, err)
	}

	// Validation and permissions.
	for _, q := range []string{"/reports/orders.csv?from=2025-01-01&to=2026-06-01", "/reports/orders.csv?status=paid",
		"/reports/ledger.csv?type=bogus", "/reports/topups.csv?status=paid", "/reports/ledger.csv?driver_id=x"} {
		if rec := get(h, q); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d", q, rec.Code)
		}
	}
	for _, role := range auth.Roles {
		rec := get(adminHandler(t, f.pool, role), "/reports/topups.csv")
		if role.Has(auth.PermReportsExport) != (rec.Code == http.StatusOK) || !role.Has(auth.PermReportsExport) && rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d", role, rec.Code)
		}
	}
}
