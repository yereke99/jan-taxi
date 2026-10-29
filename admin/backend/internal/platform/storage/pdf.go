package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/pdfdoc"
)

// MimePDF is the files.mime of stored PDF documents.
const MimePDF = "application/pdf"

// DefaultMaxPDFBytes caps stored PDFs (Kaspi receipts are about 25 KB); the
// effective limit is the smaller of this and the upload limit.
const DefaultMaxPDFBytes int64 = 5 << 20

// PDF upload errors (422), in addition to ErrFileTooLarge.
var (
	ErrNotPDF        = httpx.Unprocessable("unsupported_file_type", "the file is not a PDF document")
	ErrPDFUnreadable = httpx.Unprocessable("pdf_unreadable", "the PDF document could not be read")
)

// errPDFNotAllowed is 422 pdf_not_allowed with the reason (encrypted or the
// active-content features found).
func errPDFNotAllowed(reason string, features []string) *httpx.Error {
	d := map[string]any{"reason": reason}
	if len(features) > 0 {
		d["features"] = features
	}
	return httpx.Unprocessable("pdf_not_allowed",
		"encrypted PDFs and PDFs with scripts, attachments or data submission are not accepted").WithDetails(d)
}

// pdfLimits bound the safety check of an uploaded PDF.
var pdfLimits = pdfdoc.Limits{MaxObjects: 10_000, MaxStreamBytes: 8 << 20, MaxTotalBytes: 32 << 20, MaxPages: 20}

func (s *Store) maxPDFBytes() int64 {
	limit := DefaultMaxPDFBytes
	if s.MaxPDFBytes > 0 {
		limit = s.MaxPDFBytes
	}
	return min(limit, s.maxBytes())
}

// CheckPDF validates an uploaded PDF without storing it: the %PDF- header,
// the size limit, a readable structure, no encryption and no active content
// (JavaScript, launch actions, attachments, rich media, XFA, form submission).
func (s *Store) CheckPDF(data []byte) error {
	if int64(len(data)) > s.maxPDFBytes() {
		return ErrFileTooLarge
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return ErrNotPDF
	}
	doc, err := pdfdoc.Open(data, pdfLimits)
	if err != nil {
		return ErrPDFUnreadable.WithCause(err)
	}
	if doc.Encrypted {
		return errPDFNotAllowed("encrypted", nil)
	}
	if features := doc.ActiveContent(); len(features) > 0 {
		return errPDFNotAllowed("active_content", features)
	}
	return nil
}

// SavePDF stores a PDF byte for byte (no re-encoding: the original bytes are
// the evidence) under Dir/yyyy/mm/<uuid>.pdf with mode 0600 and inserts the
// files row through q. Only top-up receipts may be PDFs. Client-caused
// failures are 413 file_too_large, 422 unsupported_file_type,
// 422 pdf_unreadable and 422 pdf_not_allowed.
//
// When q is a transaction that later rolls back, the written file stays on
// disk without a row; RemoveOrphans deletes such files.
func (s *Store) SavePDF(ctx context.Context, q db.Querier, owner uuid.UUID, purpose string, data []byte) (File, error) {
	if purpose != PurposeTopupReceipt && purpose != PurposeIDFront && purpose != PurposeIDBack {
		return File{}, fmt.Errorf("storage: PDFs are accepted for %s only, not %q", PurposeTopupReceipt, purpose)
	}
	if len(data) == 0 {
		return File{}, ErrNotPDF
	}
	if err := s.CheckPDF(data); err != nil {
		return File{}, err
	}
	if purpose == PurposeIDFront || purpose == PurposeIDBack {
		doc, err := pdfdoc.Open(data, pdfdoc.Limits{MaxObjects: 10000, MaxStreamBytes: 8 << 20, MaxTotalBytes: 32 << 20, MaxPages: 3})
		if err != nil || !doc.InspectionComplete() {
			return File{}, ErrPDFUnreadable
		}
		if len(doc.Pages()) == 0 || len(doc.Pages()) > 2 {
			return File{}, httpx.Unprocessable("pdf_page_limit", "identity documents must have one or two pages")
		}
	}
	sum := sha256.Sum256(data)
	id := uuid.New()
	now := time.Now().UTC()
	key := path.Join(fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", int(now.Month())), id.String()+".pdf")
	if err := s.writeAtomic(key, data); err != nil {
		return File{}, fmt.Errorf("storage: write: %w", err)
	}
	f := File{
		ID:          id,
		OwnerUserID: owner,
		Purpose:     purpose,
		Mime:        MimePDF,
		SizeBytes:   int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
	}
	var ownerArg any
	if owner != uuid.Nil {
		ownerArg = owner
	}
	err := q.QueryRow(ctx, `
		INSERT INTO files (id, owner_user_id, purpose, storage_key, mime, size_bytes, sha256, width, height)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 0)
		RETURNING created_at`,
		f.ID, ownerArg, f.Purpose, key, f.Mime, f.SizeBytes, f.SHA256).Scan(&f.CreatedAt)
	if err != nil {
		_ = s.remove(key)
		return File{}, fmt.Errorf("storage: insert file row: %w", err)
	}
	return f, nil
}

// ServePDF writes a stored PDF inline with headers that keep it out of
// shared caches and stop browsers from sniffing it. It closes rc.
func ServePDF(w http.ResponseWriter, r *http.Request, f File, rc io.ReadCloser) {
	defer rc.Close()
	size := f.SizeBytes
	if st, ok := rc.(interface{ Stat() (fs.FileInfo, error) }); ok {
		if info, err := st.Stat(); err == nil {
			size = info.Size()
		}
	}
	h := w.Header()
	h.Set("Content-Type", MimePDF)
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", `inline; filename="`+f.ID.String()+`.pdf"`)
	// No "sandbox": browsers refuse to show PDFs in sandboxed documents.
	// Scripts never reach this point (SavePDF rejects them).
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	if size > 0 {
		h.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		httpx.Logger(r.Context()).Debug("serve pdf interrupted", "file_id", f.ID, "err", err)
	}
}

// ServeFile serves a stored file with the headers of its type.
func ServeFile(w http.ResponseWriter, r *http.Request, f File, rc io.ReadCloser) {
	if f.Mime == MimePDF {
		ServePDF(w, r, f, rc)
		return
	}
	ServeImage(w, r, f, rc)
}
