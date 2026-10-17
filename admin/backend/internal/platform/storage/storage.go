// Package storage keeps private user files (driver documents, avatars, vehicle
// photos, top-up receipts) on a local volume that is never served statically.
// Every image upload is sniffed, decoded and re-encoded as JPEG, which strips
// EXIF metadata including GPS position. Top-up receipts may also be PDFs,
// which are checked (no encryption, no active content) and kept byte for
// byte as payment evidence. Every file is recorded in the files table.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
)

// File purposes allowed by the files.purpose check constraint.
const (
	PurposeDriverAvatar = "driver_avatar"
	PurposeIDFront      = "id_front"
	PurposeIDBack       = "id_back"
	PurposeVehiclePhoto = "vehicle_photo"
	PurposeTopupReceipt = "topup_receipt"
)

// ValidPurpose reports whether p is a purpose accepted by the schema.
func ValidPurpose(p string) bool {
	switch p {
	case PurposeDriverAvatar, PurposeIDFront, PurposeIDBack, PurposeVehiclePhoto, PurposeTopupReceipt:
		return true
	}
	return false
}

// DefaultMaxBytes is the upload limit used when Store.MaxBytes is not set.
const DefaultMaxBytes int64 = 10 << 20

// Store saves and opens files below Dir. The zero MaxBytes means
// DefaultMaxBytes; PDFs are further capped by MaxPDFBytes (zero means
// DefaultMaxPDFBytes).
type Store struct {
	Dir         string
	MaxBytes    int64
	MaxPDFBytes int64
}

// File is the metadata of a stored file. OwnerUserID is uuid.Nil when the
// owner account no longer exists; Width and Height are 0 for PDFs.
type File struct {
	ID          uuid.UUID `json:"id"`
	OwnerUserID uuid.UUID `json:"-"`
	Purpose     string    `json:"purpose"`
	Mime        string    `json:"mime"`
	SizeBytes   int64     `json:"size_bytes"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
}

// New creates dir (mode 0700) if needed, checks that it is writable and
// returns a Store rooted at its absolute path.
func New(dir string, maxBytes int64) (*Store, error) {
	if dir == "" {
		return nil, errors.New("storage: empty directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("storage: create directory: %w", err)
	}
	probe, err := os.CreateTemp(abs, ".probe-*")
	if err != nil {
		return nil, fmt.Errorf("storage: directory is not writable: %w", err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return &Store{Dir: abs, MaxBytes: maxBytes}, nil
}

func (s *Store) maxBytes() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

// Errors returned to clients by SaveImage; compare with errors.Is.
var (
	ErrFileTooLarge    = httpx.NewError(http.StatusRequestEntityTooLarge, "file_too_large", "file is too large")
	ErrUnsupportedType = httpx.Unprocessable("unsupported_file_type", "only JPEG, PNG and WebP images are accepted")
	ErrImageTooLarge   = httpx.Unprocessable("image_too_large", "image dimensions are too large")
)

// SaveImage reads an uploaded image from r, sanitises it (see package doc),
// writes it atomically under Dir/yyyy/mm/<uuid>.jpg and inserts the files row
// through q. Client-caused failures are *httpx.Error values:
// 413 file_too_large, 422 unsupported_file_type, 422 image_too_large.
//
// When q is a transaction that later rolls back, the written file stays on disk
// without a row; RemoveOrphans deletes such files.
func (s *Store) SaveImage(ctx context.Context, q db.Querier, owner uuid.UUID, purpose string, r io.Reader) (File, error) {
	if !ValidPurpose(purpose) {
		return File{}, fmt.Errorf("storage: unknown purpose %q", purpose)
	}
	data, err := readLimited(r, s.maxBytes())
	if err != nil {
		return File{}, err
	}
	img, err := sanitize(ctx, data)
	if err != nil {
		return File{}, err
	}

	id := uuid.New()
	now := time.Now().UTC()
	key := path.Join(fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", int(now.Month())), id.String()+".jpg")
	if err := s.writeAtomic(key, img.data); err != nil {
		return File{}, fmt.Errorf("storage: write: %w", err)
	}

	f := File{
		ID:          id,
		OwnerUserID: owner,
		Purpose:     purpose,
		Mime:        "image/jpeg",
		SizeBytes:   int64(len(img.data)),
		Width:       img.width,
		Height:      img.height,
		SHA256:      img.sha256,
	}
	var ownerArg any
	if owner != uuid.Nil {
		ownerArg = owner
	}
	err = q.QueryRow(ctx, `
		INSERT INTO files (id, owner_user_id, purpose, storage_key, mime, size_bytes, sha256, width, height)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at`,
		f.ID, ownerArg, f.Purpose, key, f.Mime, f.SizeBytes, f.SHA256, f.Width, f.Height).Scan(&f.CreatedAt)
	if err != nil {
		_ = s.remove(key)
		return File{}, fmt.Errorf("storage: insert file row: %w", err)
	}
	return f, nil
}

// readLimited reads at most max bytes; more is a 413.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, ErrFileTooLarge
		}
		return nil, httpx.BadRequest("could not read the uploaded file").WithCause(err)
	}
	if int64(len(data)) > max {
		return nil, ErrFileTooLarge
	}
	if len(data) == 0 {
		return nil, ErrUnsupportedType
	}
	return data, nil
}

const selectFile = `
SELECT id, owner_user_id, purpose, storage_key, mime, size_bytes, width, height, sha256, created_at
FROM files WHERE id = $1 AND deleted_at IS NULL`

func getFile(ctx context.Context, q db.Querier, id uuid.UUID) (File, string, error) {
	var (
		f     File
		owner *uuid.UUID
		key   string
		size  int32
	)
	err := q.QueryRow(ctx, selectFile, id).Scan(&f.ID, &owner, &f.Purpose, &key, &f.Mime, &size,
		&f.Width, &f.Height, &f.SHA256, &f.CreatedAt)
	if db.IsNoRows(err) {
		return File{}, "", httpx.NotFound()
	}
	if err != nil {
		return File{}, "", fmt.Errorf("storage: load file: %w", err)
	}
	if owner != nil {
		f.OwnerUserID = *owner
	}
	f.SizeBytes = int64(size)
	return f, key, nil
}

// Get returns the metadata of a live (not soft-deleted) file, or 404.
func (s *Store) Get(ctx context.Context, q db.Querier, id uuid.UUID) (File, error) {
	f, _, err := getFile(ctx, q, id)
	return f, err
}

// Open returns the metadata and content of a live file, or 404. The caller
// must close the reader (ServeImage does).
func (s *Store) Open(ctx context.Context, q db.Querier, id uuid.UUID) (File, io.ReadCloser, error) {
	f, key, err := getFile(ctx, q, id)
	if err != nil {
		return File{}, nil, err
	}
	rc, err := s.openKey(key)
	if err != nil {
		return File{}, nil, httpx.Internal(fmt.Errorf("storage: open %s: %w", f.ID, err))
	}
	return f, rc, nil
}

// SoftDelete marks a file deleted; it stops being served immediately. The
// bytes stay on disk until removed by a retention process. Unknown or already
// deleted files yield 404.
func (s *Store) SoftDelete(ctx context.Context, q db.Querier, id uuid.UUID) error {
	tag, err := q.Exec(ctx, `UPDATE files SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("storage: soft delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return httpx.NotFound()
	}
	return nil
}
