package payments

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt"
)

// realReceipt reads the calibration Kaspi receipt kept outside the
// repository (JT_KASPI_RECEIPT_SAMPLES or the private folder next to it).
func realReceipt(t *testing.T) []byte {
	t.Helper()
	dir := os.Getenv("JT_KASPI_RECEIPT_SAMPLES")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "..", "..", "jan-taxi-private", "kaspi-receipt-samples")
	}
	data, err := os.ReadFile(filepath.Join(dir, "receipt_QR18149336524.pdf"))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("the calibration Kaspi receipt is not present (local only)")
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestRealKaspiReceiptCreditsAutomatically uploads the real receipt through
// the driver endpoint (local only): storage accepts it, the parser reads it
// and, with the top-up created just before the payment, it is credited.
func TestRealKaspiReceiptCreditsAutomatically(t *testing.T) {
	pdf := realReceipt(t)
	paidAt := time.Date(2026, 10, 17, 13, 55, 0, 0, receipt.Astana)
	if time.Since(paidAt) > 700*time.Hour {
		t.Skip("the calibration receipt is older than the longest allowed receipt age")
	}
	e := newEnv(t)
	setSetting(t, e, "topup.receipt_max_age_hours", int64(720))
	d := e.driver("approved")
	id, _ := createTopUp(t, d, 500)
	if _, err := e.pool.Exec(context.Background(), `UPDATE top_up_requests SET created_at = $2 WHERE id = $1`, id, paidAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	r := serve(t, d.h, uploadRequest(t, id, "receipt_QR18149336524.pdf", pdf))
	expect(t, r, http.StatusOK)
	rc := receiptBody(t, r)
	if r.body["status"] != StatusConfirmed || rc["decision"] != DecisionAutoConfirmed || rc["receipt_serial"] != "QR18149336524" ||
		rc["amount"].(float64) != 500 || len(reasonsOf(rc)) != 0 {
		t.Fatalf("status %v decision %v serial %v amount %v reasons %v", r.body["status"], rc["decision"], rc["receipt_serial"], rc["amount"], reasonsOf(rc))
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 500 {
		t.Fatalf("balance = %d", b)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM payment_transactions WHERE provider = 'kaspi_pdf' AND provider_transaction_id = 'QR18149336524' AND source = 'receipt_auto'`); n != 1 {
		t.Fatalf("payments = %d", n)
	}

	// The same receipt for a second top-up is a duplicate.
	again, _ := createTopUp(t, d, 500)
	dup := serve(t, d.h, uploadRequest(t, again, "receipt_QR18149336524.pdf", pdf))
	expect(t, dup, http.StatusConflict)
	if dup.errCode() != "receipt_already_used" || topUpStatus(t, e.pool, again) != StatusRejected {
		t.Fatalf("duplicate = %s", dup.raw)
	}
}

// The operator supplies an original receipt outside the repository. All money
// movement below is confined to disposable test databases, never the server.
func TestSuppliedKaspiReceipt(t *testing.T) {
	path := os.Getenv("JT_KASPI_RECEIPT_FILE")
	if path == "" {
		t.Skip("set JT_KASPI_RECEIPT_FILE to a private calibration PDF")
	}
	pdf, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("receipt unavailable")
	}
	ctx := context.Background()
	r := receipt.Parse(ctx, pdf, receipt.Options{FileName: filepath.Base(path)})
	if r.ParseStatus != receipt.StatusParsed || len(r.Blocking()) > 0 || r.Amount <= 0 || r.PaidAt == nil || r.RecipientBIN == "" || r.Serial == "" {
		t.Fatalf("receipt parsing: status=%s blocking=%v missing=%v", r.ParseStatus, r.Blocking(), r.Missing)
	}
	t.Log("original PDF parsed: amount, recipient, BIN, receipt serial and payment time present")
	e := newEnv(t)
	setSetting(t, e, "company.bin", r.RecipientBIN)
	setSetting(t, e, "kaspi.recipient_name", r.RecipientName)
	setSetting(t, e, "topup.receipt_max_age_hours", int64(720))
	d := e.driver("approved")
	id, _ := createTopUp(t, d, r.Amount)
	if _, err = e.pool.Exec(ctx, `UPDATE top_up_requests SET created_at=$2 WHERE id=$1`, id, r.PaidAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	res := serve(t, d.h, uploadRequest(t, id, filepath.Base(path), pdf))
	if res.code != http.StatusOK {
		t.Fatal("original receipt upload failed")
	}
	rc := receiptBody(t, res)
	if res.body["status"] != StatusConfirmed || rc["decision"] != DecisionAutoConfirmed {
		t.Fatalf("credit blocked: reasons=%v", reasonsOf(rc))
	}
	if balanceNow, _ := balance(t, e.pool, d.DriverID); balanceNow != r.Amount {
		t.Fatal("incorrect credited amount")
	}
	again, _ := createTopUp(t, d, r.Amount)
	dup := serve(t, d.h, uploadRequest(t, again, filepath.Base(path), pdf))
	if dup.code != http.StatusConflict || dup.errCode() != "receipt_already_used" {
		t.Fatal("receipt reused")
	}
	if balanceNow, _ := balance(t, e.pool, d.DriverID); balanceNow != r.Amount {
		t.Fatal("duplicate credited twice")
	}
}
