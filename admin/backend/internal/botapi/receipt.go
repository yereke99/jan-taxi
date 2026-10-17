package botapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/yereke99/jantaxi/admin/backend/internal/drivers"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
)

// ReceiptIntake attaches Kaspi receipts sent to the driver bot
// (*payments.Service).
type ReceiptIntake interface {
	AttachBotReceipt(ctx context.Context, d payments.ReceiptDriver, fileName string, data []byte) (payments.BotReceiptResult, error)
}

// DefaultMaxReceiptBytes bounds a receipt forwarded by the bot when
// Options.MaxReceiptBytes is not set.
const DefaultMaxReceiptBytes int64 = 5 << 20

// Receipt uploads per driver are limited (a person sends a few at most).
const (
	receiptsPerMinute = 10
	receiptBurst      = 3
)

// driverTopUpReceipt takes a receipt PDF the driver sent to the bot
// (multipart "file") and attaches it to their most recent open top-up, like
// POST /driver/wallet/topups/{id}/receipt: credited at once when every check
// passes, else left for a finance admin. Errors: 409 no_open_topup,
// 409 receipt_already_used, 413 file_too_large, 422 unsupported_file_type /
// pdf_unreadable / pdf_not_allowed.
func (h *Handler) driverTopUpReceipt(w http.ResponseWriter, r *http.Request) error {
	tgID, err := tgIDParam(r)
	if err != nil {
		return err
	}
	if h.opts.Receipts == nil {
		return httpx.NewError(http.StatusServiceUnavailable, "unavailable", "receipt intake is not configured")
	}
	ctx := r.Context()
	p, err := h.resolveDriver(ctx, tgID)
	if err != nil {
		return err
	}
	if p.Status == drivers.StatusDraft {
		return errNotRegistered()
	}
	if !h.receiptLimiter.Allow(p.ID.String()) {
		w.Header().Set("Retry-After", "10")
		return httpx.TooManyRequests()
	}
	limit := h.opts.MaxReceiptBytes
	r.Body = http.MaxBytesReader(w, r.Body, limit+64<<10)
	name, data, err := readFilePart(r, limit)
	if err != nil {
		return err
	}
	res, err := h.opts.Receipts.AttachBotReceipt(ctx, payments.ReceiptDriver{DriverID: p.ID, UserID: p.UserID}, name, data)
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

// readFilePart reads the "file" part of a multipart/form-data body.
func readFilePart(r *http.Request, limit int64) (string, []byte, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		return "", nil, httpx.NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "expected multipart/form-data")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return "", nil, httpx.BadRequest("invalid multipart body").WithCause(err)
	}
	tooLarge := func(err error) bool {
		var mbe *http.MaxBytesError
		return errors.As(err, &mbe)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return "", nil, httpx.Validation(map[string]string{"file": "required"})
		}
		if err != nil {
			if tooLarge(err) {
				return "", nil, storage.ErrFileTooLarge
			}
			return "", nil, httpx.BadRequest("invalid multipart body").WithCause(err)
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, limit+1))
		name := part.FileName()
		_ = part.Close()
		switch {
		case err != nil && tooLarge(err):
			return "", nil, storage.ErrFileTooLarge
		case err != nil:
			return "", nil, httpx.BadRequest("could not read the file").WithCause(err)
		case int64(len(data)) > limit:
			return "", nil, storage.ErrFileTooLarge
		case len(data) == 0:
			return "", nil, storage.ErrNotPDF
		}
		return name, data, nil
	}
}
