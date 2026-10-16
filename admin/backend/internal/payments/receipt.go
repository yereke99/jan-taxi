package payments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/realtime"
)

// multipartOverhead allows for multipart boundaries and headers on top of the file.
const multipartOverhead = 64 << 10

// uploadReceipt attaches a payment screenshot as evidence for the finance
// review. It never changes the balance. A new receipt replaces the shown one;
// the previous file is kept and referenced from the event history.
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
	f, err := s.saveMultipartImage(ctx, r, d.UserID)
	if err != nil {
		return err
	}

	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		t, err := getDriverTopUp(ctx, tx, d.ID, id, true)
		if err != nil {
			return err
		}
		if !t.Open() {
			return invalidTransition(t.Status, "upload_receipt")
		}
		if _, err := tx.Exec(ctx, `UPDATE top_up_requests SET receipt_file_id = $2 WHERE id = $1`, id, f.ID); err != nil {
			return fmt.Errorf("payments: attach receipt: %w", err)
		}
		meta := map[string]any{"receipt_file_id": f.ID.String()}
		if t.ReceiptFileID != nil {
			meta["previous_receipt_file_id"] = t.ReceiptFileID.String()
		}
		actor := d.ID
		if err := addEvent(ctx, tx, id, t.Status, t.Status, ActorDriver, &actor, NoteReceiptUploaded, meta); err != nil {
			return err
		}
		return publishTopUp(ctx, tx, t, realtime.TopUpUpdated, t.Status, true)
	})
	if err != nil {
		// The image row is unreferenced now; hide it so it is never served.
		_ = s.store.SoftDelete(context.WithoutCancel(ctx), s.pool, f.ID)
		return err
	}
	return s.respondDriverTopUp(w, r, d, id)
}

// saveMultipartImage streams the "file" part of a multipart/form-data body
// into storage as a top-up receipt.
func (s *Service) saveMultipartImage(ctx context.Context, r *http.Request, owner uuid.UUID) (storage.File, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		return storage.File{}, httpx.NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "expected multipart/form-data")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return storage.File{}, httpx.BadRequest("invalid multipart body").WithCause(err)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return storage.File{}, httpx.Validation(map[string]string{"file": "required"})
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return storage.File{}, storage.ErrFileTooLarge
			}
			return storage.File{}, httpx.BadRequest("invalid multipart body").WithCause(err)
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		f, err := s.store.SaveImage(ctx, s.pool, owner, storage.PurposeTopupReceipt, part)
		_ = part.Close()
		return f, err
	}
}
