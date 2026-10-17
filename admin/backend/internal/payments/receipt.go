package payments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/notifications"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
	"github.com/yereke99/jantaxi/admin/backend/internal/settings"
)

// multipartOverhead allows for multipart boundaries and headers on top of the file.
const multipartOverhead = 64 << 10

// Tolerances of the automatic receipt checks (ARCHITECTURE §6.1).
const (
	// paidBeforeTopUpSlack accepts a receipt printed shortly before the
	// top-up was created (minute precision, clock skew, paying first).
	paidBeforeTopUpSlack = 10 * time.Minute
	// paidInFutureSlack bounds clock differences with Kaspi.
	paidInFutureSlack = 5 * time.Minute
)

// Receipt outcomes returned to drivers and bots.
const (
	OutcomeCredited  = "credited"             // the receipt passed every check; the wallet is credited
	OutcomePending   = "pending_verification" // a finance admin reviews the receipt
	OutcomeDuplicate = "duplicate_rejected"   // the receipt was used before; the top-up is rejected
	OutcomeEvidence  = "evidence_attached"    // an image: kept for the admin, never credits
)

// Decisions (payment_receipts.decision).
const (
	DecisionAutoConfirmed = "auto_confirmed"
	DecisionManualReview  = "manual_review"
)

// Reasons a receipt is left for manual review (payment_receipts.reasons).
const (
	ReasonAutoConfirmDisabled = "auto_confirm_disabled"
	ReasonReceiptUnreadable   = "receipt_unreadable"
	ReasonReceiptIncomplete   = "receipt_incomplete"
	ReasonReceiptInconsistent = "receipt_inconsistent"
	ReasonTamperSuspected     = "tamper_suspected"
	ReasonRecipientMismatch   = "recipient_mismatch"
	ReasonAmountMismatch      = "amount_mismatch"
	ReasonPaidBeforeTopUp     = "paid_before_topup"
	ReasonPaidInFuture        = "paid_in_future"
	ReasonReceiptTooOld       = "receipt_too_old"
	ReasonDailyCountLimit     = "daily_count_limit"
	ReasonDailyAmountLimit    = "daily_amount_limit"
	ReasonImageReceipt        = "image_receipt"
)

// RejectDuplicateReceipt is top_up_requests.reject_reason of a top-up whose
// receipt was already used.
const RejectDuplicateReceipt = "duplicate_receipt"

// contentFlags are receipt flags about the printed data (ambiguous or
// inconsistent fields); every other blocking flag is about the file itself.
var contentFlags = map[string]bool{
	receipt.FlagSerialAmbiguous: true, receipt.FlagSerialLabelMissing: true, receipt.FlagSerialFormat: true,
	receipt.FlagAmountAmbiguous: true, receipt.FlagAmountFractional: true, receipt.FlagStatusMissing: true,
	receipt.FlagFileNameMismatch: true, receipt.FlagQRMismatch: true,
}

// ReceiptDriver is the driver attaching a receipt.
type ReceiptDriver struct {
	DriverID uuid.UUID
	UserID   uuid.UUID
}

// upload is a received receipt file.
type upload struct {
	data     []byte
	fileName string
}

func (u upload) isPDF() bool { return bytes.HasPrefix(u.data, []byte("%PDF-")) }

// attachResult is what attaching a receipt did.
type attachResult struct {
	outcome   string
	reasons   []string
	topupID   uuid.UUID
	balance   *int64
	duplicate bool
}

// errReceiptAlreadyUsed is 409 receipt_already_used. It never names the
// top-up or driver that used the receipt first.
func errReceiptAlreadyUsed(topupID *uuid.UUID, status string) *httpx.Error {
	d := map[string]any{"reason": RejectDuplicateReceipt}
	if topupID != nil {
		d["topup_id"] = topupID.String()
	}
	if status != "" {
		d["status"] = status
	}
	return httpx.Conflict("receipt_already_used", "this receipt was already used").WithDetails(d)
}

// errNoOpenTopUp is 409 no_open_topup: a receipt arrived but the driver has
// no top-up waiting for payment.
var errNoOpenTopUp = httpx.Conflict("no_open_topup", "create a top-up first, then send its receipt")

// uploadReceipt attaches a receipt to the top-up. A Kaspi PDF receipt is
// checked and, when it passes every check, credits the wallet at once;
// otherwise the top-up waits for a finance admin (pending_verification). An
// image is evidence for the admin only. A receipt used before rejects the
// top-up (409 receipt_already_used).
func (s *Service) uploadReceipt(w http.ResponseWriter, r *http.Request) error {
	d, err := requireWalletDriver(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	t, err := getDriverTopUp(ctx, s.pool, d.ID, id, false)
	if err != nil {
		return err
	}
	if !t.Open() {
		return invalidTransition(t.Status, "upload_receipt")
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+multipartOverhead)
	up, err := s.readMultipartFile(r)
	if err != nil {
		return err
	}
	res, err := s.attach(ctx, ReceiptDriver{DriverID: d.ID, UserID: d.UserID}, id, up)
	if err != nil {
		return err
	}
	if res.duplicate {
		return errReceiptAlreadyUsed(&id, StatusRejected)
	}
	return s.respondDriverTopUp(w, r, d, id)
}

// readMultipartFile reads the "file" part of a multipart/form-data body.
func (s *Service) readMultipartFile(r *http.Request) (upload, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		return upload{}, httpx.NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "expected multipart/form-data")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return upload{}, httpx.BadRequest("invalid multipart body").WithCause(err)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return upload{}, httpx.Validation(map[string]string{"file": "required"})
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return upload{}, storage.ErrFileTooLarge
			}
			return upload{}, httpx.BadRequest("invalid multipart body").WithCause(err)
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, s.maxUpload+1))
		name := part.FileName()
		_ = part.Close()
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return upload{}, storage.ErrFileTooLarge
			}
			return upload{}, httpx.BadRequest("could not read the uploaded file").WithCause(err)
		}
		if int64(len(data)) > s.maxUpload {
			return upload{}, storage.ErrFileTooLarge
		}
		if len(data) == 0 {
			return upload{}, storage.ErrUnsupportedType
		}
		return upload{data: data, fileName: name}, nil
	}
}

// attach stores the receipt and applies it to top-up id (owned by d).
func (s *Service) attach(ctx context.Context, d ReceiptDriver, id uuid.UUID, up upload) (attachResult, error) {
	if !up.isPDF() {
		return s.attachImage(ctx, d, id, up)
	}
	parsed, err := s.checkAndParse(ctx, up)
	if err != nil {
		return attachResult{}, err
	}
	return s.attachPDF(ctx, d, id, up, parsed)
}

// pdfSlots bounds concurrent PDF checks: one hostile upload may decode up to
// tens of MiB, so a burst must not exhaust memory.
var pdfSlots = make(chan struct{}, 4)

// checkAndParse validates an uploaded PDF (storage rules) and parses it as a
// Kaspi receipt.
func (s *Service) checkAndParse(ctx context.Context, up upload) (receipt.Receipt, error) {
	select {
	case pdfSlots <- struct{}{}:
		defer func() { <-pdfSlots }()
	case <-ctx.Done():
		return receipt.Receipt{}, ctx.Err()
	}
	if err := s.store.CheckPDF(up.data); err != nil {
		return receipt.Receipt{}, err
	}
	parseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return receipt.Parse(parseCtx, up.data, receipt.Options{FileName: up.fileName}), nil
}

// attachImage keeps an image as evidence for the finance review. It never
// changes the balance or the status.
func (s *Service) attachImage(ctx context.Context, d ReceiptDriver, id uuid.UUID, up upload) (attachResult, error) {
	f, err := s.store.SaveImage(ctx, s.pool, d.UserID, storage.PurposeTopupReceipt, bytes.NewReader(up.data))
	if err != nil {
		return attachResult{}, err
	}
	res := attachResult{outcome: OutcomeEvidence, reasons: []string{ReasonImageReceipt}, topupID: id}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := getDriverTopUp(ctx, tx, d.DriverID, id, true)
		if err != nil {
			return err
		}
		if !t.Open() {
			return invalidTransition(t.Status, "upload_receipt")
		}
		if _, err := tx.Exec(ctx, `UPDATE top_up_requests SET receipt_file_id = $2 WHERE id = $1`, id, f.ID); err != nil {
			return fmt.Errorf("payments: attach receipt: %w", err)
		}
		meta := map[string]any{"receipt_file_id": f.ID.String(), "kind": "image"}
		if t.ReceiptFileID != nil {
			meta["previous_receipt_file_id"] = t.ReceiptFileID.String()
		}
		actor := d.DriverID
		if err := addEvent(ctx, tx, id, t.Status, t.Status, ActorDriver, &actor, NoteReceiptUploaded, meta); err != nil {
			return err
		}
		return publishTopUp(ctx, tx, t, realtime.TopUpUpdated, t.Status, true)
	})
	if err != nil {
		// The image row is unreferenced now; hide it so it is never served.
		_ = s.store.SoftDelete(context.WithoutCancel(ctx), s.pool, f.ID)
		return attachResult{}, err
	}
	return res, nil
}

// errReplay rolls back an upload of a receipt the top-up already has.
var errReplay = errors.New("payments: receipt already attached to this top-up")

// attachPDF applies a parsed Kaspi receipt in one transaction: store the
// file, reject a receipt used before, record it (the unique indexes of
// payment_receipts are the final guard against reuse, also under
// concurrency), evaluate the automatic checks and either credit through
// confirmTopUp or leave the top-up for review.
func (s *Service) attachPDF(ctx context.Context, d ReceiptDriver, id uuid.UUID, up upload, parsed receipt.Receipt) (attachResult, error) {
	res := attachResult{topupID: id}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		// One writer per driver: daily limits and replays hold under concurrency.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, db.LockKey("topup:"+d.DriverID.String())); err != nil {
			return fmt.Errorf("payments: lock driver top-ups: %w", err)
		}
		t, err := getDriverTopUp(ctx, tx, d.DriverID, id, true)
		if err != nil {
			return err
		}
		if !t.Open() {
			return invalidTransition(t.Status, "upload_receipt")
		}
		conflict, err := findReceiptConflict(ctx, tx, parsed)
		if err != nil {
			return err
		}
		if conflict != nil && conflict.topupID != nil && *conflict.topupID == t.ID {
			return errReplay
		}
		f, err := s.store.SavePDF(ctx, tx, d.UserID, storage.PurposeTopupReceipt, up.data)
		if err != nil {
			return err
		}
		if conflict != nil {
			res.outcome, res.duplicate = OutcomeDuplicate, true
			return s.rejectDuplicate(ctx, tx, t, f, parsed, conflict)
		}
		decision, reasons, err := evaluateReceipt(ctx, tx, t, parsed, s.kaspi.fallbackRecipient)
		if err != nil {
			return err
		}
		receiptID, inserted, err := insertReceipt(ctx, tx, t, f, parsed, decision, reasons)
		if err != nil {
			return err
		}
		if !inserted {
			// A concurrent upload recorded the same receipt first.
			if conflict, err = findReceiptConflict(ctx, tx, parsed); err != nil {
				return err
			}
			if conflict == nil {
				return fmt.Errorf("payments: receipt conflict vanished")
			}
			res.outcome, res.duplicate = OutcomeDuplicate, true
			return s.rejectDuplicate(ctx, tx, t, f, parsed, conflict)
		}
		if _, err := tx.Exec(ctx, `UPDATE top_up_requests SET receipt_file_id = $2, payment_receipt_id = $3 WHERE id = $1`,
			t.ID, f.ID, receiptID); err != nil {
			return fmt.Errorf("payments: attach receipt: %w", err)
		}
		next := t.Status
		if decision == DecisionManualReview {
			next = StatusPendingVerification
		}
		meta := map[string]any{"receipt_file_id": f.ID.String(), "receipt_id": receiptID.String(), "kind": "pdf",
			"decision": decision, "reasons": reasons}
		if t.ReceiptFileID != nil {
			meta["previous_receipt_file_id"] = t.ReceiptFileID.String()
		}
		actor := d.DriverID
		if err := addEvent(ctx, tx, t.ID, t.Status, next, ActorDriver, &actor, NoteReceiptUploaded, meta); err != nil {
			return err
		}
		res.reasons = reasons
		if decision == DecisionAutoConfirmed {
			payer := nonEmpty(parsed.PayerMasked)
			wal, _, err := confirmTopUp(ctx, tx, nil, t, ConfirmInput{
				VerifiedAmount: parsed.Amount, ProviderTransactionID: parsed.Serial, PaidAt: *parsed.PaidAt, PayerInfo: payer,
			}, confirmer{provider: receipt.Provider, source: SourceReceiptAuto, receiptID: &receiptID})
			if err != nil {
				return err
			}
			res.outcome, res.balance = OutcomeCredited, &wal.Balance
			return nil
		}
		res.outcome = OutcomePending
		if t.Status == StatusCreated {
			if _, err := tx.Exec(ctx, `
				UPDATE top_up_requests SET status = 'pending_verification', driver_reported_paid_at = COALESCE(driver_reported_paid_at, now())
				WHERE id = $1`, t.ID); err != nil {
				return fmt.Errorf("payments: submit top-up: %w", err)
			}
		}
		return publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusPendingVerification, true)
	})
	switch {
	case errors.Is(err, errReplay):
		// The same receipt again (a retry): report the current state.
		return s.currentResult(ctx, id)
	case err != nil:
		return attachResult{}, err
	}
	if res.duplicate {
		s.log.Warn("kaspi receipt reused", slog.String("topup_id", id.String()), slog.String("driver_id", d.DriverID.String()),
			slog.String("receipt_serial", parsed.Serial))
	}
	return res, nil
}

// currentResult describes a top-up after a replayed upload.
func (s *Service) currentResult(ctx context.Context, id uuid.UUID) (attachResult, error) {
	t, err := GetTopUp(ctx, s.pool, id, false)
	if err != nil {
		return attachResult{}, err
	}
	res := attachResult{topupID: id, outcome: OutcomePending}
	if v, err := latestReceipt(ctx, s.pool, t); err == nil && v != nil {
		res.reasons = v.reasons
	}
	if t.Status == StatusConfirmed {
		res.outcome = OutcomeCredited
	}
	return res, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// receiptConflict is an earlier use of the same receipt.
type receiptConflict struct {
	receiptID *uuid.UUID
	paymentID *uuid.UUID
	topupID   *uuid.UUID
	matched   []string // file_sha256, receipt_serial, qr_payload, payment_transaction
}

// findReceiptConflict looks for an earlier receipt with the same file, number
// or QR payload, or a recorded Kaspi payment with the same number.
func findReceiptConflict(ctx context.Context, q db.Querier, r receipt.Receipt) (*receiptConflict, error) {
	var (
		c                         receiptConflict
		rid, tid                  uuid.UUID
		sameFile, sameNum, sameQR bool
	)
	err := q.QueryRow(ctx, `
		SELECT id, topup_id, file_sha256 = $1, COALESCE(receipt_serial = $2, false), COALESCE(qr_payload_hash = $3, false)
		FROM payment_receipts
		WHERE file_sha256 = $1 OR (provider = $4 AND receipt_serial = $2) OR qr_payload_hash = $3
		ORDER BY created_at LIMIT 1`, r.FileSHA256, nullString(r.Serial), nullString(r.QRPayloadHash), receipt.Provider).
		Scan(&rid, &tid, &sameFile, &sameNum, &sameQR)
	switch {
	case err == nil:
		c.receiptID, c.topupID = &rid, &tid
		if sameFile {
			c.matched = append(c.matched, "file_sha256")
		}
		if sameNum {
			c.matched = append(c.matched, "receipt_serial")
		}
		if sameQR {
			c.matched = append(c.matched, "qr_payload")
		}
		return &c, nil
	case !db.IsNoRows(err):
		return nil, fmt.Errorf("payments: find receipt: %w", err)
	}
	if r.Serial == "" {
		return nil, nil
	}
	var (
		pid   uuid.UUID
		topup *uuid.UUID
	)
	err = q.QueryRow(ctx, `
		SELECT id, topup_id FROM payment_transactions
		WHERE provider IN ('kaspi_manual', 'kaspi_pdf') AND provider_transaction_id = $1`, r.Serial).Scan(&pid, &topup)
	switch {
	case err == nil:
		return &receiptConflict{paymentID: &pid, topupID: topup, matched: []string{"payment_transaction"}}, nil
	case !db.IsNoRows(err):
		return nil, fmt.Errorf("payments: find payment: %w", err)
	}
	return nil, nil
}

// rejectDuplicate closes the top-up as rejected (duplicate_receipt), keeping
// the uploaded file as evidence, and tells the driver.
func (s *Service) rejectDuplicate(ctx context.Context, tx pgx.Tx, t TopUp, f storage.File, parsed receipt.Receipt, c *receiptConflict) error {
	if _, err := tx.Exec(ctx, `
		UPDATE top_up_requests SET status = 'rejected', rejected_by = NULL, rejected_at = now(), reject_reason = $2,
		       receipt_file_id = $3
		WHERE id = $1`, t.ID, RejectDuplicateReceipt, f.ID); err != nil {
		return fmt.Errorf("payments: reject duplicate: %w", err)
	}
	meta := map[string]any{"receipt_file_id": f.ID.String(), "matched": c.matched, "file_sha256": parsed.FileSHA256}
	if parsed.Serial != "" {
		meta["receipt_serial"] = parsed.Serial
	}
	if c.receiptID != nil {
		meta["existing_receipt_id"] = c.receiptID.String()
	}
	if c.paymentID != nil {
		meta["existing_payment_transaction_id"] = c.paymentID.String()
	}
	if c.topupID != nil {
		meta["existing_topup_id"] = c.topupID.String()
	}
	if err := addEvent(ctx, tx, t.ID, t.Status, StatusRejected, ActorSystem, nil, NoteDuplicateReceipt, meta); err != nil {
		return err
	}
	if err := audit.Log(ctx, tx, nil, audit.Entry{
		Action: "topup.reject_duplicate_receipt", EntityType: "topup", EntityID: t.ID.String(),
		Before: map[string]any{"status": t.Status, "reference": t.Reference},
		After:  map[string]any{"status": StatusRejected, "reason": RejectDuplicateReceipt, "evidence": meta},
	}); err != nil {
		return err
	}
	lang := driverLang(ctx, tx, t.DriverUserID)
	if err := notifications.Enqueue(ctx, tx, notifications.Notification{
		Bot: notifications.DriverBot, UserID: t.DriverUserID, Template: notifications.TplTopUpRejected,
		Params:    map[string]any{"topup_id": t.ID.String(), "reference": t.Reference, "reason": duplicateReasonText[lang]},
		DedupeKey: "topup_rejected:" + t.ID.String(),
	}); err != nil {
		return fmt.Errorf("payments: enqueue topup_rejected: %w", err)
	}
	return publishTopUp(ctx, tx, t, realtime.TopUpUpdated, StatusRejected, true)
}

var duplicateReasonText = map[string]string{
	"kk": "бұл Kaspi чегі бұрын пайдаланылған",
	"ru": "этот чек Kaspi уже был использован",
	"en": "this Kaspi receipt was already used",
}

// evaluateReceipt applies the automatic crediting conditions. An empty
// reason list means the top-up may be credited now. fallbackSeller
// (KASPI_PAY_RECIPIENT_NAME) names the seller when the settings do not.
func evaluateReceipt(ctx context.Context, tx pgx.Tx, t TopUp, r receipt.Receipt, fallbackSeller string) (string, []string, error) {
	var reasons []string
	add := func(reason string) {
		if !slices.Contains(reasons, reason) {
			reasons = append(reasons, reason)
		}
	}
	policy := settings.ReceiptAutoConfirm(ctx, tx)
	if !policy.Enabled {
		add(ReasonAutoConfirmDisabled)
	}
	switch r.ParseStatus {
	case receipt.StatusUnreadable:
		add(ReasonReceiptUnreadable)
	case receipt.StatusPartial:
		add(ReasonReceiptIncomplete)
	}
	for _, f := range r.Blocking() {
		switch {
		case f == receipt.FlagUnreadable:
			add(ReasonReceiptUnreadable)
		case contentFlags[f]:
			add(ReasonReceiptInconsistent)
		default:
			add(ReasonTamperSuspected)
		}
	}
	company := settings.CompanyDetails(ctx, tx)
	if strings.TrimSpace(company.Name) == "" {
		company.Name = fallbackSeller
	}
	// The seller BIN identifies the payee; a receipt without one is never
	// credited automatically (every calibrated receipt prints it), and a
	// printed name must not contradict it.
	switch {
	case r.RecipientBIN != "":
		if r.RecipientBIN != company.BIN {
			add(ReasonRecipientMismatch)
		}
	case r.ParseStatus != receipt.StatusUnreadable:
		add(ReasonReceiptIncomplete)
		if r.RecipientName != "" && !receipt.SellerMatches(r.RecipientName, company.Name) {
			add(ReasonRecipientMismatch)
		}
	}
	if r.Amount > 0 && r.Amount != t.RequestedAmount {
		add(ReasonAmountMismatch)
	}
	now := time.Now()
	if r.PaidAt != nil {
		switch {
		case r.PaidAt.Before(t.CreatedAt.Add(-paidBeforeTopUpSlack)):
			add(ReasonPaidBeforeTopUp)
		case r.PaidAt.After(now.Add(paidInFutureSlack)):
			add(ReasonPaidInFuture)
		}
		if now.Sub(*r.PaidAt) > policy.ReceiptMaxAge {
			add(ReasonReceiptTooOld)
		}
	}
	count, sum, err := autoCreditsToday(ctx, tx, t.DriverID, now)
	if err != nil {
		return "", nil, err
	}
	if count+1 > policy.MaxPerDay {
		add(ReasonDailyCountLimit)
	}
	if sum+r.Amount > policy.MaxAmountPerDay {
		add(ReasonDailyAmountLimit)
	}
	if len(reasons) == 0 {
		return DecisionAutoConfirmed, []string{}, nil
	}
	return DecisionManualReview, reasons, nil
}

// autoCreditsToday counts the driver's automatic credits since the start of
// the business day (Asia/Aqtobe), refunded ones included.
func autoCreditsToday(ctx context.Context, q db.Querier, driverID uuid.UUID, now time.Time) (int64, int64, error) {
	local := now.In(businessLoc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, businessLoc)
	var count, sum int64
	err := q.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(p.amount), 0)
		FROM payment_transactions p JOIN top_up_requests t ON t.id = p.topup_id
		WHERE p.source = 'receipt_auto' AND t.driver_id = $1 AND p.created_at >= $2`, driverID, start).Scan(&count, &sum)
	if err != nil {
		return 0, 0, fmt.Errorf("payments: count automatic credits: %w", err)
	}
	return count, sum, nil
}

// insertReceipt records the receipt; inserted is false when one of the
// unique indexes (file, number, QR payload) already holds it.
func insertReceipt(ctx context.Context, tx pgx.Tx, t TopUp, f storage.File, r receipt.Receipt, decision string, reasons []string) (uuid.UUID, bool, error) {
	var amount any
	if r.Amount > 0 {
		amount = r.Amount
	}
	flags := r.Flags
	if flags == nil {
		flags = []string{}
	}
	if reasons == nil {
		reasons = []string{}
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO payment_receipts (provider, file_id, file_sha256, qr_payload_hash, receipt_serial, amount, paid_at,
		                              recipient_name, recipient_bin, payer_masked, payment_method, producer,
		                              parse_status, flags, decision, reasons, driver_id, topup_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		receipt.Provider, f.ID, r.FileSHA256, nullString(r.QRPayloadHash), nullString(r.Serial), amount, r.PaidAt,
		nullString(r.RecipientName), nullString(r.RecipientBIN), nullString(r.PayerMasked), nullString(r.PaymentMethod),
		nullString(r.Producer), r.ParseStatus, flags, decision, reasons, t.DriverID, t.ID).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("payments: insert receipt: %w", err)
	}
	return id, true, nil
}

// linkedReceiptRow is the part of the top-up's latest receipt the admin
// confirmation needs.
type linkedReceiptRow struct {
	id     uuid.UUID
	serial *string
}

func linkedReceipt(ctx context.Context, q db.Querier, t TopUp) (*linkedReceiptRow, error) {
	if t.PaymentReceiptID == nil {
		return nil, nil
	}
	var r linkedReceiptRow
	err := q.QueryRow(ctx, `SELECT id, receipt_serial FROM payment_receipts WHERE id = $1`, *t.PaymentReceiptID).Scan(&r.id, &r.serial)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("payments: load receipt: %w", err)
	}
	return &r, nil
}

// AttachBotReceipt attaches a receipt sent to the driver bot to one of the
// driver's open top-ups (created or pending_verification): the one that
// already holds this receipt (a re-send), else the most recent one whose
// amount equals the receipt's, else the most recent one. Without an open
// top-up it answers 409 receipt_already_used when the receipt was used
// before, else 409 no_open_topup.
func (s *Service) AttachBotReceipt(ctx context.Context, d ReceiptDriver, fileName string, data []byte) (BotReceiptResult, error) {
	if int64(len(data)) > s.maxUpload {
		return BotReceiptResult{}, storage.ErrFileTooLarge
	}
	up := upload{data: data, fileName: fileName}
	if !up.isPDF() {
		return BotReceiptResult{}, storage.ErrNotPDF
	}
	parsed, err := s.checkAndParse(ctx, up)
	if err != nil {
		return BotReceiptResult{}, err
	}
	id, err := botTopUpFor(ctx, s.pool, d.DriverID, parsed)
	if errors.Is(err, errNoOpenTopUp) {
		c, cerr := findReceiptConflict(ctx, s.pool, parsed)
		if cerr != nil {
			return BotReceiptResult{}, cerr
		}
		if c != nil {
			return BotReceiptResult{}, errReceiptAlreadyUsed(nil, "")
		}
		return BotReceiptResult{}, errNoOpenTopUp
	}
	if err != nil {
		return BotReceiptResult{}, err
	}
	res, err := s.attachPDF(ctx, d, id, up, parsed)
	if err != nil {
		return BotReceiptResult{}, err
	}
	t, err := GetTopUp(ctx, s.pool, id, false)
	if err != nil {
		return BotReceiptResult{}, err
	}
	out := BotReceiptResult{
		Outcome: res.outcome, Reasons: driverReasons(res.reasons), Balance: res.balance,
		TopUp: BotTopUp{ID: t.ID, Reference: t.Reference, Amount: t.RequestedAmount, VerifiedAmount: t.VerifiedAmount, Status: t.Status},
	}
	if res.duplicate {
		return out, errReceiptAlreadyUsed(&id, StatusRejected)
	}
	return out, nil
}

// botTopUpFor picks the open top-up a receipt sent to the bot belongs to
// (see AttachBotReceipt); errNoOpenTopUp when the driver has none.
func botTopUpFor(ctx context.Context, q db.Querier, driverID uuid.UUID, parsed receipt.Receipt) (uuid.UUID, error) {
	rows, err := q.Query(ctx, `
		SELECT id, requested_amount FROM top_up_requests
		WHERE driver_id = $1 AND status IN ('created', 'pending_verification')
		ORDER BY created_at DESC, number DESC LIMIT $2`, driverID, 4*MaxOpenTopUps)
	if err != nil {
		return uuid.Nil, fmt.Errorf("payments: find open top-ups: %w", err)
	}
	type open struct {
		id     uuid.UUID
		amount int64
	}
	var list []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.amount); err != nil {
			rows.Close()
			return uuid.Nil, fmt.Errorf("payments: scan open top-up: %w", err)
		}
		list = append(list, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return uuid.Nil, fmt.Errorf("payments: find open top-ups: %w", err)
	}
	if len(list) == 0 {
		return uuid.Nil, errNoOpenTopUp
	}
	c, err := findReceiptConflict(ctx, q, parsed)
	if err != nil {
		return uuid.Nil, err
	}
	for _, o := range list {
		if c != nil && c.topupID != nil && *c.topupID == o.id {
			return o.id, nil
		}
	}
	for _, o := range list {
		if parsed.Amount > 0 && o.amount == parsed.Amount {
			return o.id, nil
		}
	}
	return list[0].id, nil
}

// BotReceiptResult is the internal API answer to a receipt sent to the bot.
type BotReceiptResult struct {
	Outcome string   `json:"outcome"`
	Reasons []string `json:"reasons"`
	TopUp   BotTopUp `json:"topup"`
	Balance *int64   `json:"balance"`
}

// BotTopUp is the top-up a bot receipt was attached to.
type BotTopUp struct {
	ID             uuid.UUID `json:"id"`
	Reference      string    `json:"reference"`
	Amount         int64     `json:"amount"`
	VerifiedAmount *int64    `json:"verified_amount"`
	Status         string    `json:"status"`
}

// driverReasons are the reasons a driver may see; file checks stay coarse
// (tamper_suspected) so the checks cannot be probed.
func driverReasons(reasons []string) []string {
	out := []string{}
	for _, r := range reasons {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}
