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
	"github.com/yereke99/jantaxi/admin/backend/internal/vehicles"
)

// Upload kinds (OpenAPI UploadKind).
const (
	KindAvatar       = "avatar"
	KindIDFront      = "id_front"
	KindIDBack       = "id_back"
	KindVehicleMain  = "vehicle_main"
	KindVehicleBack  = "vehicle_back"
	KindVehicleSide  = "vehicle_side"
	KindVehiclePlate = "vehicle_plate"
	KindVehicleExtra = "vehicle_extra"
)

type uploadSpec struct {
	purpose   string // files.purpose
	field     string // registration field the upload belongs to
	photoKind string // vehicle_photos.kind for vehicle photos
}

var uploadKinds = map[string]uploadSpec{
	KindAvatar:       {storage.PurposeDriverAvatar, FieldAvatar, ""},
	KindIDFront:      {storage.PurposeIDFront, FieldIDFront, ""},
	KindIDBack:       {storage.PurposeIDBack, FieldIDBack, ""},
	KindVehicleMain:  {storage.PurposeVehiclePhoto, FieldVehiclePhotos, vehicles.PhotoMain},
	KindVehicleBack:  {storage.PurposeVehiclePhoto, FieldVehiclePhotos, vehicles.PhotoBack},
	KindVehicleSide:  {storage.PurposeVehiclePhoto, FieldVehiclePhotos, vehicles.PhotoSide},
	KindVehiclePlate: {storage.PurposeVehiclePhoto, FieldVehiclePhotos, vehicles.PhotoPlate},
	KindVehicleExtra: {storage.PurposeVehiclePhoto, FieldVehiclePhotos, vehicles.PhotoExtra},
}

// multipartOverhead is allowed on top of the file size for headers and the kind field.
const multipartOverhead = 64 << 10

var errPayloadTooLarge = httpx.NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")

// uploadFile stores one registration photo. The image is decoded and saved
// before the transaction (so no row lock is held while it is processed) and
// linked inside it after the editability checks are repeated under lock.
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
	// Cheap checks first so a locked registration does not upload 10 MiB in vain.
	prof, err := GetProfile(ctx, h.pool, d.ID)
	if err != nil {
		return err
	}
	if !prof.Editable() {
		return ErrRegistrationLocked(prof.Status)
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.opt.MaxUploadBytes+multipartOverhead)
	kind, data, err := readUpload(r, h.opt.MaxUploadBytes)
	if err != nil {
		return err
	}
	spec, ok := uploadKinds[kind]
	if !ok {
		return httpx.Validation(map[string]string{"kind": "invalid"})
	}
	if err := checkUploadAllowed(ctx, h.pool, prof, spec); err != nil {
		return err
	}

	var f storage.File
	if (spec.purpose == storage.PurposeIDFront || spec.purpose == storage.PurposeIDBack) && bytes.HasPrefix(data, []byte("%PDF-")) {
		f, err = h.store.SavePDF(ctx, h.pool, p.UserID, spec.purpose, data)
	} else {
		f, err = h.store.SaveImage(ctx, h.pool, p.UserID, spec.purpose, bytes.NewReader(data))
	}
	if err != nil {
		return err
	}
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		locked, err := LockProfile(ctx, tx, d.ID)
		if err != nil {
			return err
		}
		if err := checkUploadAllowed(ctx, tx, locked, spec); err != nil {
			return err
		}
		return linkUpload(ctx, tx, locked, spec, f.ID)
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

// checkUploadAllowed enforces editability and, for extra photos, the limit.
func checkUploadAllowed(ctx context.Context, q db.Querier, prof Profile, spec uploadSpec) error {
	if !prof.Editable() {
		return ErrRegistrationLocked(prof.Status)
	}
	if prof.Status == StatusChangesRequested {
		flagged, err := FlaggedFields(ctx, q, prof.ID)
		if err != nil {
			return err
		}
		if !flagged[spec.field] {
			return ErrFieldLocked(map[string]string{"kind": "not_editable"}, flagged)
		}
	}
	if spec.photoKind == vehicles.PhotoExtra {
		v, err := vehicles.Current(ctx, q, prof.ID)
		if errors.Is(err, vehicles.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var n int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM vehicle_photos WHERE vehicle_id = $1 AND kind = 'extra' AND is_current`, v.ID).
			Scan(&n); err != nil {
			return fmt.Errorf("drivers: count photos: %w", err)
		}
		if n >= vehicles.MaxExtraPhotos {
			return vehicles.ErrTooManyPhotos
		}
	}
	return nil
}

// linkUpload makes fileID the current file of its kind (the previous one is
// kept as history).
func linkUpload(ctx context.Context, tx pgx.Tx, prof Profile, spec uploadSpec, fileID uuid.UUID) error {
	switch spec.purpose {
	case storage.PurposeDriverAvatar:
		if _, err := tx.Exec(ctx, `UPDATE driver_profiles SET avatar_file_id = $2 WHERE id = $1`, prof.ID, fileID); err != nil {
			return fmt.Errorf("drivers: set avatar: %w", err)
		}
		return nil
	case storage.PurposeIDFront, storage.PurposeIDBack:
		if _, err := tx.Exec(ctx, `UPDATE driver_documents SET is_current = false WHERE driver_id = $1 AND kind = $2 AND is_current`,
			prof.ID, spec.purpose); err != nil {
			return fmt.Errorf("drivers: replace document: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO driver_documents (driver_id, kind, file_id) VALUES ($1, $2, $3)`,
			prof.ID, spec.purpose, fileID); err != nil {
			return fmt.Errorf("drivers: add document: %w", err)
		}
		return nil
	}
	v, err := vehicles.CurrentForUpdate(ctx, tx, prof.ID)
	if errors.Is(err, vehicles.ErrNotFound) {
		v, err = vehicles.Create(ctx, tx, prof.ID, vehicles.Details{})
	}
	if err != nil {
		return err
	}
	return vehicles.AddPhoto(ctx, tx, v.ID, spec.photoKind, fileID)
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

// deleteFile removes an extra vehicle photo while the registration is editable.
func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) error {
	d, err := auth.RequireDriver(r)
	if err != nil {
		return err
	}
	fileID, err := httpx.UUIDParam(r, "fileId")
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = db.WithTx(ctx, h.pool, func(tx pgx.Tx) error {
		prof, err := LockProfile(ctx, tx, d.ID)
		if err != nil {
			return err
		}
		if !prof.Editable() {
			return ErrRegistrationLocked(prof.Status)
		}
		if prof.Status == StatusChangesRequested {
			flagged, err := FlaggedFields(ctx, tx, prof.ID)
			if err != nil {
				return err
			}
			if !flagged[FieldVehiclePhotos] {
				return ErrFieldLocked(map[string]string{FieldVehiclePhotos: "not_editable"}, flagged)
			}
		}
		v, err := vehicles.CurrentForUpdate(ctx, tx, prof.ID)
		if err != nil {
			return err
		}
		kind, err := vehicles.PhotoKindOf(ctx, tx, v.ID, fileID)
		if err != nil {
			return err
		}
		if kind != vehicles.PhotoExtra {
			return httpx.Conflict("conflict", "required photos are replaced by uploading a new one").
				WithDetails(map[string]any{"reason": "not_removable"})
		}
		if err := vehicles.RemoveExtraPhoto(ctx, tx, v.ID, fileID); err != nil {
			return err
		}
		// Files that were part of a submitted application stay as evidence.
		var submitted bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM driver_applications WHERE driver_id = $1 AND snapshot->'file_ids' ? $2)`,
			prof.ID, fileID.String()).Scan(&submitted); err != nil {
			return fmt.Errorf("drivers: snapshot lookup: %w", err)
		}
		if !submitted {
			return h.store.SoftDelete(ctx, tx, fileID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
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
