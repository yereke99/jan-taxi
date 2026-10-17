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
