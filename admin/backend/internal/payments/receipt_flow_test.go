package payments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt/receipttest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// newSerial returns a fresh Kaspi receipt number.
func newSerial() string { return fmt.Sprintf("QR%011d", rand.Int64N(1e11)) }

// kaspiSpec is a valid receipt for amount (printed with a thousands space).
func kaspiSpec(amount int64) receipttest.Spec {
	s := receipttest.Default()
	s.Serial = newSerial()
	s.Amount = groupThousands(amount)
	return s
}

func groupThousands(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

func uploadRequest(t *testing.T, id uuid.UUID, name string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/driver/wallet/topups/"+id.String()+"/receipt", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func uploadPDF(t *testing.T, d driver, id uuid.UUID, pdf []byte) resp {
	t.Helper()
	return serve(t, d.h, uploadRequest(t, id, "receipt.pdf", pdf))
}

func receiptBody(t *testing.T, r resp) map[string]any {
	t.Helper()
	rc, _ := r.body["receipt"].(map[string]any)
	if rc == nil {
		t.Fatalf("no receipt in %s", r.raw)
	}
	return rc
}

func reasonsOf(rc map[string]any) []string {
	var out []string
	for _, r := range rc["reasons"].([]any) {
		out = append(out, r.(string))
	}
	return out
}

func setSetting(t *testing.T, e *env, key string, value any) {
	t.Helper()
	if err := settings.Set(context.Background(), e.pool, key, value, uuid.Nil); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptCreditsAutomatically(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	ctx := context.Background()
	id, _ := createTopUp(t, d, 5000)
	spec := kaspiSpec(5000)
	pdf := receipttest.Build(spec)

	listener := listen(t, e)
	r := uploadPDF(t, d, id, pdf)
	expect(t, r, http.StatusOK)
	rc := receiptBody(t, r)
	if r.body["status"] != StatusConfirmed || r.body["verified_amount"].(float64) != 5000 || rc["kind"] != "pdf" ||
		rc["decision"] != DecisionAutoConfirmed || len(reasonsOf(rc)) != 0 || rc["receipt_serial"] != spec.Serial {
		t.Fatalf("upload = %s", r.raw)
	}
	listener.expect(t, realtime.WalletUpdated, realtime.DriverScope(d.DriverID))
	listener.expect(t, realtime.TopUpConfirmed, realtime.DriverScope(d.DriverID))
	if b, _ := balance(t, e.pool, d.DriverID); b != 5000 {
		t.Fatalf("balance = %d", b)
	}
	// Same ledger path as an admin confirmation: one credit, keyed by the top-up.
	if n := count(t, e.pool, `SELECT count(*) FROM wallet_ledger WHERE idempotency_key = $1 AND entry_type = 'topup_credit' AND balance_delta = 5000 AND admin_id IS NULL`,
		"topup:"+id.String()); n != 1 {
		t.Fatalf("ledger credits = %d", n)
	}
	var (
		provider, txn, source string
		receiptID, recordedBy *uuid.UUID
		payer                 *string
	)
	if err := e.pool.QueryRow(ctx, `SELECT provider, provider_transaction_id, source, receipt_id, recorded_by, payer_info FROM payment_transactions WHERE topup_id = $1`, id).
		Scan(&provider, &txn, &source, &receiptID, &recordedBy, &payer); err != nil {
		t.Fatal(err)
	}
	if provider != "kaspi_pdf" || txn != spec.Serial || source != SourceReceiptAuto || receiptID == nil || recordedBy != nil ||
		payer == nil || *payer != "Т*** Т." {
		t.Fatalf("payment = %s %s %s %v %v %v", provider, txn, source, receiptID, recordedBy, payer)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM top_up_requests WHERE id = $1 AND confirmed_by IS NULL AND confirmed_by_system AND payment_receipt_id = $2`, id, *receiptID); n != 1 {
		t.Fatal("top-up not confirmed by the system")
	}
	if n := count(t, e.pool, `SELECT count(*) FROM top_up_events WHERE topup_id = $1 AND to_status = 'confirmed' AND actor_type = 'system' AND note = $2`, id, NoteAutoConfirmed); n != 1 {
		t.Fatalf("system confirm events = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM admin_audit_logs WHERE action = 'topup.auto_confirm' AND entity_id = $1 AND admin_id IS NULL`, id.String()); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM notification_outbox WHERE template = 'topup_confirmed' AND dedupe_key = $1`, "topup_confirmed:"+id.String()); n != 1 {
		t.Fatalf("notifications = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM payment_receipts WHERE topup_id = $1 AND decision = 'auto_confirmed' AND parse_status = 'parsed'
		AND receipt_serial = $2 AND amount = 5000 AND recipient_bin = '000809500289' AND payer_masked = 'Т*** Т.' AND flags = '["qr_not_found"]'`, id, spec.Serial); n != 1 {
		t.Fatalf("receipt rows = %d", n)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM files f JOIN payment_receipts r ON r.file_id = f.id WHERE r.topup_id = $1 AND f.mime = 'application/pdf' AND f.sha256 = r.file_sha256`, id); n != 1 {
		t.Fatal("pdf file row missing")
	}

	// Admins see the receipt and the PDF itself, byte for byte.
	det := do(t, fin, http.MethodGet, "/topups/"+id.String(), nil)
	expect(t, det, http.StatusOK)
	adm := det.body["receipt"].(map[string]any)
	if det.body["confirmed_automatically"] != true || adm["receipt_serial"] != spec.Serial || adm["decision"] != DecisionAutoConfirmed ||
		len(det.body["receipts"].([]any)) != 1 || det.body["confirm_prefill"].(map[string]any)["verified_amount"].(float64) != 5000 {
		t.Fatalf("admin detail = %s", det.raw)
	}
	file := do(t, fin, http.MethodGet, "/topups/"+id.String()+"/receipt", nil)
	expect(t, file, http.StatusOK)
	if file.hdr.Get("Content-Type") != "application/pdf" || file.hdr.Get("Cache-Control") != "private, no-store" ||
		file.hdr.Get("Content-Disposition") != `inline; filename="`+adm["file_id"].(string)+`.pdf"` || !bytes.Equal(file.raw, pdf) {
		t.Fatalf("receipt file headers %v (%d bytes)", file.hdr, len(file.raw))
	}

	// The top-up is closed: the same receipt cannot be attached again.
	again := uploadPDF(t, d, id, pdf)
	expect(t, again, http.StatusConflict)
	if again.errCode() != "invalid_transition" {
		t.Fatalf("re-upload = %s", again.raw)
	}

	// The automatic credit is reversible through the refund flow.
	ref := do(t, fin, http.MethodPost, "/topups/"+id.String()+"/refund", map[string]any{"reason": "Kaspi payment returned"})
	expect(t, ref, http.StatusOK)
	if b, _ := balance(t, e.pool, d.DriverID); b != 0 {
		t.Fatalf("balance after refund = %d", b)
	}
}

func TestReceiptReuseIsRejected(t *testing.T) {
	e := newEnv(t)
	a := e.driver("approved")
	b := e.driver("approved")
	spec := kaspiSpec(1000)
	pdf := receipttest.Build(spec)
	idA, _ := createTopUp(t, a, 1000)
	expect(t, uploadPDF(t, a, idA, pdf), http.StatusOK)

	check := func(name string, d driver, data []byte) {
		t.Helper()
		id, _ := createTopUp(t, d, 1000)
		r := uploadPDF(t, d, id, data)
		expect(t, r, http.StatusConflict)
		if r.errCode() != "receipt_already_used" || r.errDetails()["reason"] != RejectDuplicateReceipt ||
			r.errDetails()["status"] != StatusRejected || r.errDetails()["topup_id"] != id.String() {
			t.Fatalf("%s: %s", name, r.raw)
		}
		if _, leaked := r.errDetails()["existing_topup_id"]; leaked {
			t.Fatalf("%s: the first top-up leaked: %s", name, r.raw)
		}
		var status, reason string
		if err := e.pool.QueryRow(context.Background(), `SELECT status, reject_reason FROM top_up_requests WHERE id = $1`, id).Scan(&status, &reason); err != nil {
			t.Fatal(err)
		}
		if status != StatusRejected || reason != RejectDuplicateReceipt {
			t.Fatalf("%s: top-up %s %s", name, status, reason)
		}
		if n := count(t, e.pool, `SELECT count(*) FROM top_up_events WHERE topup_id = $1 AND to_status = 'rejected' AND actor_type = 'system' AND note = $2`, id, NoteDuplicateReceipt); n != 1 {
			t.Fatalf("%s: events = %d", name, n)
		}
		if n := count(t, e.pool, `SELECT count(*) FROM notification_outbox WHERE template = 'topup_rejected' AND dedupe_key = $1 AND params->>'reason' <> ''`, "topup_rejected:"+id.String()); n != 1 {
			t.Fatalf("%s: notifications = %d", name, n)
		}
		if n := count(t, e.pool, `SELECT count(*) FROM admin_audit_logs WHERE action = 'topup.reject_duplicate_receipt' AND entity_id = $1`, id.String()); n != 1 {
			t.Fatalf("%s: audit = %d", name, n)
		}
		// The rejected top-up keeps the file as evidence for admins.
		if n := count(t, e.pool, `SELECT count(*) FROM top_up_requests t JOIN files f ON f.id = t.receipt_file_id WHERE t.id = $1 AND f.mime = 'application/pdf'`, id); n != 1 {
			t.Fatalf("%s: evidence file missing", name)
		}
	}
	// The identical file, by another driver.
	check("same file", b, pdf)
	// Another rendering of the same receipt number (different bytes).
	other := spec
	other.Payer = "Басқа Б."
	check("same receipt number", b, receipttest.Build(other))
	// The same driver reusing the receipt for a second top-up.
	check("same driver", a, pdf)

	// The same QR payload under another number.
	q1 := kaspiSpec(1000)
	q1.QRPayload = "https://kaspi.kz/check/7f3a9c"
	idQ, _ := createTopUp(t, a, 1000)
	expect(t, uploadPDF(t, a, idQ, receipttest.Build(q1)), http.StatusOK)
	q2 := kaspiSpec(1000)
	q2.QRPayload = q1.QRPayload
	check("same qr payload", b, receipttest.Build(q2))

	if bal, _ := balance(t, e.pool, b.DriverID); bal != 0 {
		t.Fatalf("driver b balance = %d", bal)
	}
	if bal, _ := balance(t, e.pool, a.DriverID); bal != 2000 {
		t.Fatalf("driver a balance = %d", bal)
	}
}

func TestReceiptUniquenessIsEnforcedByTheDatabase(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	id, _ := createTopUp(t, d, 1000)
	spec := kaspiSpec(1000)
	expect(t, uploadPDF(t, d, id, receipttest.Build(spec)), http.StatusOK)
	ctx := context.Background()
	var fileID, receiptID uuid.UUID
	var sha string
	if err := e.pool.QueryRow(ctx, `SELECT id, file_id, file_sha256 FROM payment_receipts WHERE topup_id = $1`, id).Scan(&receiptID, &fileID, &sha); err != nil {
		t.Fatal(err)
	}
	insert := func(fileSHA, serial, qr any) error {
		// A second file row so only the tested column can collide.
		var f uuid.UUID
		if err := e.pool.QueryRow(ctx, `
			INSERT INTO files (owner_user_id, purpose, storage_key, mime, size_bytes, sha256, width, height)
			VALUES ($1, 'topup_receipt', $2, 'application/pdf', 10, $3, 0, 0) RETURNING id`,
			d.UserID, "2026/10/"+uuid.NewString()+".pdf", fmt.Sprint(fileSHA)).Scan(&f); err != nil {
			t.Fatal(err)
		}
		_, err := e.pool.Exec(ctx, `
			INSERT INTO payment_receipts (provider, file_id, file_sha256, qr_payload_hash, receipt_serial, parse_status, decision, driver_id, topup_id)
			VALUES ('kaspi_pdf', $1, $2, $3, $4, 'parsed', 'manual_review', $5, $6)`, f, fileSHA, qr, serial, d.DriverID, id)
		return err
	}
	hex := func(c byte) string { return string(bytes.Repeat([]byte{c}, 64)) }
	if err := insert(sha, nil, nil); !db.IsUniqueViolation(err, "payment_receipts_file_sha256_key") {
		t.Fatalf("same file: %v", err)
	}
	if err := insert(hex('a'), spec.Serial, nil); !db.IsUniqueViolation(err, "payment_receipts_serial_key") {
		t.Fatalf("same serial: %v", err)
	}
	if err := insert(hex('b'), nil, hex('c')); err != nil {
		t.Fatalf("first qr: %v", err)
	}
	if err := insert(hex('d'), nil, hex('c')); !db.IsUniqueViolation(err, "payment_receipts_qr_payload_key") {
		t.Fatalf("same qr: %v", err)
	}
	// Receipts without number or QR do not collide with each other.
	if err := insert(hex('e'), nil, nil); err != nil {
		t.Fatalf("no serial: %v", err)
	}
	// Receipts are append-only evidence.
	if _, err := e.pool.Exec(ctx, `UPDATE payment_receipts SET amount = 1 WHERE id = $1`, receiptID); err == nil {
		t.Fatal("payment_receipts must be append-only")
	}
	// One Kaspi payment number across the manual and receipt providers.
	_, err := e.pool.Exec(ctx, `INSERT INTO payment_transactions (provider, provider_transaction_id, amount, source) VALUES ('kaspi_manual', $1, 1000, 'manual_admin')`, spec.Serial)
	if !db.IsUniqueViolation(err, "payment_transactions_kaspi_txn_key") {
		t.Fatalf("kaspi number across providers: %v", err)
	}
}

func TestConcurrentUploadsOfOneReceipt(t *testing.T) {
	e := newEnv(t)
	const n = 6
	pdf := receipttest.Build(kaspiSpec(2000))
	drivers := make([]driver, n)
	ids := make([]uuid.UUID, n)
	for i := range drivers {
		drivers[i] = e.driver("approved")
		ids[i], _ = createTopUp(t, drivers[i], 2000)
	}
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		codes = make([]int, n)
	)
	for i := range drivers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			drivers[i].h.ServeHTTP(rec, uploadRequest(t, ids[i], "receipt.pdf", pdf))
			codes[i] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()
	ok, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflicts++
		}
	}
	if ok != 1 || conflicts != n-1 {
		t.Fatalf("codes = %v, want exactly one 200", codes)
	}
	if c := count(t, e.pool, `SELECT count(*) FROM wallet_ledger WHERE entry_type = 'topup_credit'`); c != 1 {
		t.Fatalf("credits = %d", c)
	}
	if c := count(t, e.pool, `SELECT count(*) FROM payment_receipts`); c != 1 {
		t.Fatalf("receipts = %d", c)
	}
	if c := count(t, e.pool, `SELECT count(*) FROM top_up_requests WHERE status = 'rejected' AND reject_reason = 'duplicate_receipt'`); c != n-1 {
		t.Fatalf("rejected = %d", c)
	}
}

func TestReceiptLeftForReview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	now := time.Now()
	tests := []struct {
		name   string
		amount int64 // top-up amount
		pdf    func() []byte
		setup  func(id uuid.UUID)
		want   []string
	}{
		{name: "amount differs", amount: 5000, pdf: func() []byte { return receipttest.Build(kaspiSpec(500)) },
			want: []string{ReasonAmountMismatch}},
		{name: "other seller BIN", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(1000)
			s.BIN = "990340000276"
			return receipttest.Build(s)
		}, want: []string{ReasonRecipientMismatch}},
		{name: "other seller name without BIN", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(1000)
			s.NoBINLine, s.Seller = true, `ИП "Сауле"`
			return receipttest.Build(s)
		}, want: []string{ReasonRecipientMismatch}},
		{name: "paid before the top-up", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(1000)
			s.PaidAt = now.Add(-time.Hour)
			return receipttest.Build(s)
		}, want: []string{ReasonPaidBeforeTopUp}},
		{name: "paid in the future", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(1000)
			s.PaidAt = now.Add(time.Hour)
			return receipttest.Build(s)
		}, want: []string{ReasonPaidInFuture}},
		{name: "stale receipt", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(1000)
			s.PaidAt = now.Add(-49 * time.Hour)
			return receipttest.Build(s)
		}, setup: func(id uuid.UUID) {
			if _, err := e.pool.Exec(ctx, `UPDATE top_up_requests SET created_at = now() - interval '72 hours' WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}, want: []string{ReasonReceiptTooOld}},
		{name: "edited after download", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(500)
			edited := s
			edited.Amount = "1 000"
			return receipttest.BuildEdited(s, edited)
		}, want: []string{ReasonTamperSuspected}},
		{name: "saved by another program", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(1000)
			s.Producer = "Skia/PDF m120"
			return receipttest.Build(s)
		}, want: []string{ReasonTamperSuspected}},
		{name: "not paid", amount: 1000, pdf: func() []byte {
			s := kaspiSpec(1000)
			s.Status = "Платёж отклонён"
			return receipttest.Build(s)
		}, want: []string{ReasonReceiptIncomplete, ReasonReceiptInconsistent}},
		{name: "kill switch", amount: 1000, pdf: func() []byte { return receipttest.Build(kaspiSpec(1000)) },
			setup: func(uuid.UUID) { setSetting(t, e, "topup.auto_confirm_enabled", false) },
			want:  []string{ReasonAutoConfirmDisabled}},
		{name: "daily amount limit", amount: 60000, pdf: func() []byte { return receipttest.Build(kaspiSpec(60000)) },
			setup: func(uuid.UUID) { setSetting(t, e, "topup.auto_confirm_enabled", true) },
			want:  []string{ReasonDailyAmountLimit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := e.driver("approved")
			id, _ := createTopUp(t, d, tt.amount)
			if tt.setup != nil {
				tt.setup(id)
			}
			r := uploadPDF(t, d, id, tt.pdf())
			expect(t, r, http.StatusOK)
			rc := receiptBody(t, r)
			if r.body["status"] != StatusPendingVerification || rc["decision"] != DecisionManualReview ||
				!slices.Equal(reasonsOf(rc), tt.want) {
				t.Fatalf("status %v decision %v reasons %v, want %v", r.body["status"], rc["decision"], reasonsOf(rc), tt.want)
			}
			if b, _ := balance(t, e.pool, d.DriverID); b != 0 {
				t.Fatalf("balance = %d", b)
			}
			if n := count(t, e.pool, `SELECT count(*) FROM payment_transactions WHERE topup_id = $1`, id); n != 0 {
				t.Fatalf("payments = %d", n)
			}
			if n := count(t, e.pool, `SELECT count(*) FROM top_up_events WHERE topup_id = $1 AND note = 'receipt_uploaded' AND to_status = 'pending_verification' AND meta->>'decision' = 'manual_review'`, id); n != 1 {
				t.Fatalf("events = %d", n)
			}
		})
	}
}

func TestReceiptDailyCountLimit(t *testing.T) {
	e := newEnv(t)
	setSetting(t, e, "topup.auto_max_per_day", int64(2))
	d := e.driver("approved")
	for i := 0; i < 3; i++ {
		id, _ := createTopUp(t, d, 1000)
		r := uploadPDF(t, d, id, receipttest.Build(kaspiSpec(1000)))
		expect(t, r, http.StatusOK)
		want := StatusConfirmed
		if i == 2 {
			want = StatusPendingVerification
			if got := reasonsOf(receiptBody(t, r)); !slices.Equal(got, []string{ReasonDailyCountLimit}) {
				t.Fatalf("reasons = %v", got)
			}
		}
		if r.body["status"] != want {
			t.Fatalf("upload %d: status %v", i, r.body["status"])
		}
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 2000 {
		t.Fatalf("balance = %d", b)
	}
}

func TestReviewedReceiptConfirmedByAdmin(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	other := e.driver("approved")
	_, fin := e.admin(auth.RoleFinanceManager)
	id, _ := createTopUp(t, d, 5000)
	spec := kaspiSpec(5000)
	setSetting(t, e, "topup.auto_confirm_enabled", false)
	expect(t, uploadPDF(t, d, id, receipttest.Build(spec)), http.StatusOK)

	det := do(t, fin, http.MethodGet, "/topups/"+id.String(), nil)
	expect(t, det, http.StatusOK)
	pre := det.body["confirm_prefill"].(map[string]any)
	if pre["provider_transaction_id"] != spec.Serial || pre["verified_amount"].(float64) != 5000 || pre["paid_at"] == nil ||
		!slices.Contains(det.body["receipt"].(map[string]any)["reasons"].([]any), any(ReasonAutoConfirmDisabled)) {
		t.Fatalf("detail = %s", det.raw)
	}

	// Another top-up cannot be confirmed with this receipt's number.
	oid, _ := createTopUp(t, other, 5000)
	used := confirm(t, fin, oid, 5000, spec.Serial)
	expect(t, used, http.StatusConflict)
	if used.errCode() != "payment_reference_used" || used.errDetails()["topup_id"] != id.String() {
		t.Fatalf("other top-up = %s", used.raw)
	}

	c := do(t, fin, http.MethodPost, "/topups/"+id.String()+"/confirm", map[string]any{
		"verified_amount": pre["verified_amount"], "provider_transaction_id": pre["provider_transaction_id"],
		"paid_at": pre["paid_at"], "payer_info": pre["payer_info"],
	})
	expect(t, c, http.StatusOK)
	pt := c.body["payment_transaction"].(map[string]any)
	if c.body["status"] != StatusConfirmed || c.body["confirmed_automatically"] != false || pt["provider"] != "kaspi_pdf" || pt["source"] != SourceManualAdmin {
		t.Fatalf("confirmed = %s", c.raw)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 5000 {
		t.Fatalf("balance = %d", b)
	}
}

func TestImageReceiptNeverCredits(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	id, _ := createTopUp(t, d, 1000)
	r := serve(t, d.h, multipartRequest(t, "/driver/wallet/topups/"+id.String()+"/receipt", "file", pngImage(t)))
	expect(t, r, http.StatusOK)
	rc := receiptBody(t, r)
	if r.body["status"] != StatusCreated || rc["kind"] != "image" || !slices.Equal(reasonsOf(rc), []string{ReasonImageReceipt}) || r.body["has_receipt"] != true {
		t.Fatalf("image upload = %s", r.raw)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM payment_receipts`); n != 0 {
		t.Fatalf("receipts = %d", n)
	}
	if b, _ := balance(t, e.pool, d.DriverID); b != 0 {
		t.Fatalf("balance = %d", b)
	}
}

func TestReceiptReplayKeepsTheTopUp(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	id, _ := createTopUp(t, d, 5000)
	pdf := receipttest.Build(kaspiSpec(500)) // amount differs: stays pending
	expect(t, uploadPDF(t, d, id, pdf), http.StatusOK)
	again := uploadPDF(t, d, id, pdf)
	expect(t, again, http.StatusOK)
	if again.body["status"] != StatusPendingVerification {
		t.Fatalf("replay = %s", again.raw)
	}
	if n := count(t, e.pool, `SELECT count(*) FROM payment_receipts WHERE topup_id = $1`, id); n != 1 {
		t.Fatalf("receipts = %d", n)
	}
}

func TestReceiptFileChecks(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	id, _ := createTopUp(t, d, 1000)
	js := []byte("%PDF-1.7\n1 0 obj <</Type /Catalog /Pages 2 0 R /OpenAction 3 0 R>> endobj\n2 0 obj <</Type /Pages /Kids []>> endobj\n3 0 obj <</S /JavaScript /JS (app.alert(1))>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF")
	r := uploadPDF(t, d, id, js)
	expect(t, r, http.StatusUnprocessableEntity)
	if r.errCode() != "pdf_not_allowed" || r.errDetails()["reason"] != "active_content" {
		t.Fatalf("javascript = %s", r.raw)
	}
	enc := append(receipttest.Build(kaspiSpec(1000)), []byte("\ntrailer <</Root 2 0 R /Encrypt <</Filter /Standard /V 2>>>>\n%%EOF\n")...)
	r = uploadPDF(t, d, id, enc)
	expect(t, r, http.StatusUnprocessableEntity)
	if r.errCode() != "pdf_not_allowed" || r.errDetails()["reason"] != "encrypted" {
		t.Fatalf("encrypted = %s", r.raw)
	}
	r = uploadPDF(t, d, id, []byte("not a receipt at all"))
	expect(t, r, http.StatusUnprocessableEntity)
	if r.errCode() != "unsupported_file_type" {
		t.Fatalf("garbage = %s", r.raw)
	}
	if st := topUpStatus(t, e.pool, id); st != StatusCreated {
		t.Fatalf("status = %s", st)
	}
}

func TestBotReceiptIntake(t *testing.T) {
	e := newEnv(t)
	d := e.driver("approved")
	rd := ReceiptDriver{DriverID: d.DriverID, UserID: d.UserID}
	ctx := context.Background()
	pdf := receipttest.Build(kaspiSpec(1000))

	_, err := e.svc.AttachBotReceipt(ctx, rd, "receipt.pdf", pdf)
	if code := httpCode(err); code != "no_open_topup" {
		t.Fatalf("no top-up: %v", err)
	}
	older, _ := createTopUp(t, d, 3000)
	id, _ := createTopUp(t, d, 1000)
	res, err := e.svc.AttachBotReceipt(ctx, rd, "receipt.pdf", pdf)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != OutcomeCredited || res.TopUp.ID != id || res.Balance == nil || *res.Balance != 1000 || res.TopUp.Status != StatusConfirmed {
		t.Fatalf("result = %+v", res)
	}
	if st := topUpStatus(t, e.pool, older); st != StatusCreated {
		t.Fatalf("older top-up = %s", st)
	}
	// Sent again: the open older top-up must not swallow a used receipt.
	res, err = e.svc.AttachBotReceipt(ctx, rd, "receipt.pdf", pdf)
	if httpCode(err) != "receipt_already_used" || res.TopUp.ID != older || res.Outcome != OutcomeDuplicate {
		t.Fatalf("again: %+v %v", res, err)
	}
	// Without an open top-up a used receipt is still recognised.
	_, err = e.svc.AttachBotReceipt(ctx, rd, "receipt.pdf", pdf)
	if httpCode(err) != "receipt_already_used" {
		t.Fatalf("no open top-up, used receipt: %v", err)
	}
	if _, err := e.svc.AttachBotReceipt(ctx, rd, "photo.jpg", pngImage(t)); httpCode(err) != "unsupported_file_type" {
		t.Fatalf("image: %v", err)
	}
}

// httpCode returns the API error code of err ("" for other errors).
func httpCode(err error) string {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}
