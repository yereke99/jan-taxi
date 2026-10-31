package drivers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
)

// KindAvatar is the only registration upload (OpenAPI UploadKind): ID scans
// and vehicle photos are no longer collected. Files uploaded before stay
// stored and viewable.
const KindAvatar = "avatar"

// MaxAvatarBytes caps one profile photo (the Mini App downscales before
// uploading, so real photos are far smaller).
const MaxAvatarBytes int64 = 5 << 20

// multipartOverhead is allowed on top of the file size for headers and the kind field.
const multipartOverhead = 64 << 10

var errPayloadTooLarge = httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")

// uploadFile stores the profile photo. The image is sniffed by its magic
// bytes (JPEG, PNG or WebP), decoded and saved as JPEG before the transaction
// (so no row lock is held while it is processed) and linked inside it after
// the editability checks are repeated under lock.
func (h *Handler) uploadFile(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "multipart/form-data" {
		return errNotMultipart
	}
	ctx := r.Context()
	// Cheap checks first so a locked registration does not upload in vain.
	prof, err := GetProfile(ctx, h.pool, d.ID)
	if err != nil {
		return err
	}
	if !prof.Editable() {
		return ErrRegistrationLocked(prof.Status)
	}

	limit := min(h.opt.MaxUploadBytes, MaxAvatarBytes)
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartOverhead)
	kind, data, err := readUpload(r, limit)
	if err != nil {
		return err
	}
	if kind != KindAvatar {
		return httpx.Validation(map[string]string{"kind": "invalid"})
	}
	if err := checkAvatarEditable(ctx, h.pool, prof); err != nil {
		return err
	}

	f, err := h.store.SaveImage(ctx, h.pool, p.UserID, storage.PurposeDriverAvatar, bytes.NewReader(data))
	if err != nil {
		return err
	}
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		locked, err := LockProfile(ctx, tx, d.ID)
		if err != nil {
			return err
		}
		if err := checkAvatarEditable(ctx, tx, locked); err != nil {
			return err
		}
		// The previous photo stays as history.
		if _, err := tx.Exec(ctx, `UPDATE driver_profiles SET avatar_file_id = $2 WHERE id = $1`, locked.ID, f.ID); err != nil {
			return fmt.Errorf("drivers: set avatar: %w", err)
		}
		return nil
	})
	if err != nil {
		// The stored image is not referenced by anything; stop serving it.
		if delErr := h.store.SoftDelete(context.WithoutCancel(ctx), h.pool, f.ID); delErr != nil {
			httpx.Logger(ctx).Warn("discard unlinked upload", slog.String("file_id", f.ID.String()), slog.Any("err", delErr))
		}
		return err
	}
	return httpx.Created(w, FileRef{
		ID: f.ID, Kind: kind, URL: DriverFileURL + f.ID.String(), Mime: f.Mime, Width: f.Width, Height: f.Height, CreatedAt: f.CreatedAt.UTC(),
	})
}

// checkAvatarEditable enforces editability: any draft, and changes_requested
// only when the moderator flagged the photo.
func checkAvatarEditable(ctx context.Context, q db.Querier, prof Profile) error {
	if !prof.Editable() {
		return ErrRegistrationLocked(prof.Status)
	}
	editable, err := editableFieldsOf(ctx, q, prof)
	if err != nil {
		return err
	}
	if !editable[FieldAvatar] {
		return ErrFieldLocked(map[string]string{"kind": "not_editable"}, editable)
	}
	return nil
}

// readUpload reads the "kind" and "file" parts of a multipart body.
func readUpload(r *http.Request, maxFile int64) (string, []byte, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", nil, httpx.BadRequest("invalid multipart body").WithCause(err)
	}
	var (
		kind    string
		data    []byte
		hasFile bool
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, multipartError(err)
		}
		switch part.FormName() {
		case "kind":
			b, err := io.ReadAll(io.LimitReader(part, 64))
			if err != nil {
				return "", nil, multipartError(err)
			}
			kind = strings.TrimSpace(string(b))
		case "file":
			if hasFile {
				_ = part.Close()
				return "", nil, httpx.BadRequest("only one file per request")
			}
			hasFile = true
			data, err = io.ReadAll(io.LimitReader(part, maxFile+1))
			if err != nil {
				return "", nil, multipartError(err)
			}
			if int64(len(data)) > maxFile {
				_ = part.Close()
				return "", nil, storage.ErrFileTooLarge
			}
		default:
			_ = part.Close()
			return "", nil, httpx.BadRequest("unexpected form field " + part.FormName())
		}
		_ = part.Close()
	}
	fields := map[string]string{}
	if kind == "" {
		fields["kind"] = "required"
	}
	if !hasFile {
		fields["file"] = "required"
	}
	if len(fields) > 0 {
		return "", nil, httpx.Validation(fields)
	}
	return kind, data, nil
}

func multipartError(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return errPayloadTooLarge
	}
	return httpx.BadRequest("invalid multipart body").WithCause(err)
}

// getOwnFile serves one of the caller's own files; anything else is 404.
func (h *Handler) getOwnFile(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	meta, err := h.store.Get(r.Context(), h.pool, id)
	if err != nil {
		return err
	}
	if meta.OwnerUserID == uuid.Nil || meta.OwnerUserID != p.UserID {
		return httpx.NotFound()
	}
	return h.serve(w, r, id)
}

// getOrderDriverPhoto serves the avatar of the driver assigned to one of the
// passenger's orders.
func (h *Handler) getOrderDriverPhoto(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.RequirePrincipal(r)
	if err != nil {
		return err
	}
	orderID, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var fileID *uuid.UUID
	err = h.pool.QueryRow(r.Context(), `
		SELECT dp.avatar_file_id FROM orders o JOIN driver_profiles dp ON dp.id = o.driver_id
		WHERE o.id = $1 AND o.passenger_id = $2`, orderID, p.UserID).Scan(&fileID)
	if db.IsNoRows(err) || (err == nil && fileID == nil) {
		return httpx.NotFound()
	}
	if err != nil {
		return fmt.Errorf("drivers: order driver photo: %w", err)
	}
	return h.serve(w, r, *fileID)
}

// getAdminFile serves a private file to an administrator. ID documents need
// documents.view and every view is audit-logged; avatars and vehicle photos
// need drivers.view; top-up receipts need finance.view.
func (h *Handler) getAdminFile(w http.ResponseWriter, r *http.Request) error {
	a, err := auth.RequireAdminPrincipal(r)
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	meta, err := h.store.Get(ctx, h.pool, id)
	if err != nil {
		return err
	}
	perm, ok := filePermission(meta.Purpose)
	if !ok || !a.Can(perm) {
		return httpx.Forbidden()
	}
	f, rc, err := h.store.Open(ctx, h.pool, id)
	if err != nil {
		return err
	}
	if f.Purpose == storage.PurposeIDFront || f.Purpose == storage.PurposeIDBack {
		// The view is recorded before any byte is sent; no audit row, no image.
		if err := h.auditDocumentView(r, f); err != nil {
			_ = rc.Close()
			return err
		}
	}
	storage.ServeFile(w, r, f, rc)
	return nil
}

// auditDocumentView records that the current admin opened an ID document.
func (h *Handler) auditDocumentView(r *http.Request, f storage.File) error {
	ctx := r.Context()
	entityType, entityID := "file", f.ID.String()
	after := map[string]any{"file_id": f.ID, "purpose": f.Purpose}
	if f.OwnerUserID != uuid.Nil {
		driverID, err := IDForUser(ctx, h.pool, f.OwnerUserID)
		switch {
		case err == nil:
			entityType, entityID = "driver", driverID.String()
		case !errors.Is(err, ErrNotFound):
			return err
		}
	}
	return audit.Log(ctx, h.pool, r, audit.Entry{Action: "document.view", EntityType: entityType, EntityID: entityID, After: after})
}

func filePermission(purpose string) (auth.Permission, bool) {
	switch purpose {
	case storage.PurposeIDFront, storage.PurposeIDBack:
		return auth.PermDocumentsView, true
	case storage.PurposeDriverAvatar, storage.PurposeVehiclePhoto:
		return auth.PermDriversView, true
	case storage.PurposeTopupReceipt:
		return auth.PermFinanceView, true
	}
	return "", false
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, id uuid.UUID) error {
	f, rc, err := h.store.Open(r.Context(), h.pool, id)
	if err != nil {
		return err
	}
	storage.ServeFile(w, r, f, rc)
	return nil
}
