package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/httpx"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// --- image builders -----------------------------------------------------------

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = uint8(x*255/w), uint8(y*255/h), 128, 255
		}
	}
	return img
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// exifSegment builds an APP1 Exif segment whose IFD0 holds an Orientation tag
// and a fake GPS entry pointing at a marker string.
func exifSegment(order binary.ByteOrder, orientation uint16) []byte {
	var t bytes.Buffer
	if order == binary.LittleEndian {
		t.WriteString("II")
	} else {
		t.WriteString("MM")
	}
	u16 := func(v uint16) { _ = binary.Write(&t, order, v) }
	u32 := func(v uint32) { _ = binary.Write(&t, order, v) }
	u16(42)
	u32(8) // IFD0 offset
	u16(2) // entries
	// Orientation, SHORT, count 1, value left-justified.
	u16(0x0112)
	u16(3)
	u32(1)
	u16(orientation)
	u16(0)
	// GPSInfo IFD pointer (LONG) to the marker data after the IFD.
	u16(0x8825)
	u16(4)
	u32(1)
	u32(8 + 2 + 2*12 + 4)
	u32(0) // next IFD
	t.WriteString("GPS-SECRET-LOCATION-48.8264N-58.1442E")

	payload := append([]byte("Exif\x00\x00"), t.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

func withEXIF(jpg, seg []byte) []byte {
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// segments lists JPEG marker bytes up to the start of scan.
func segments(t *testing.T, b []byte) []byte {
	t.Helper()
	var markers []byte
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			t.Fatalf("bad marker at %d", i)
		}
		m := b[i+1]
		markers = append(markers, m)
		if m == 0xDA {
			break
		}
		i += 2 + int(binary.BigEndian.Uint16(b[i+2:i+4]))
	}
	return markers
}

// --- helpers ------------------------------------------------------------------

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "files"), 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func wantHTTPError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != status || he.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
}

// --- tests --------------------------------------------------------------------

func TestSaveImagePNG(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()

	f, err := s.SaveImage(ctx, pool, owner, PurposeDriverAvatar, bytes.NewReader(encodePNG(t, gradient(300, 200))))
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}
	if f.Width != 300 || f.Height != 200 || f.Mime != "image/jpeg" || f.Purpose != PurposeDriverAvatar || f.OwnerUserID != owner {
		t.Fatalf("unexpected file: %+v", f)
	}
	if f.CreatedAt.IsZero() || time.Since(f.CreatedAt) > time.Minute {
		t.Fatalf("created_at: %v", f.CreatedAt)
	}

	var key string
	if err := pool.QueryRow(ctx, `SELECT storage_key FROM files WHERE id = $1`, f.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	wantKey := now.Format("2006") + "/" + now.Format("01") + "/" + f.ID.String() + ".jpg"
	if key != wantKey {
		t.Fatalf("storage key %q, want %q", key, wantKey)
	}
	full := filepath.Join(s.Dir, filepath.FromSlash(key))
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if sniff(data) != formatJPEG {
		t.Fatal("stored file is not a JPEG")
	}
	sum := sha256.Sum256(data)
	if f.SHA256 != hex.EncodeToString(sum[:]) || f.SizeBytes != int64(len(data)) {
		t.Fatal("sha256/size mismatch")
	}
	if st, _ := os.Stat(full); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, want 0600", st.Mode().Perm())
	}
	for _, d := range []string{filepath.Dir(full), filepath.Dir(filepath.Dir(full))} {
		if st, _ := os.Stat(d); st.Mode().Perm() != 0o700 {
			t.Fatalf("dir %s mode %v, want 0700", d, st.Mode().Perm())
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(full)); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestSaveImageStripsEXIF(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)

	src := withEXIF(encodeJPEG(t, gradient(64, 48)), exifSegment(binary.BigEndian, 1))
	if !bytes.Contains(src, []byte("GPS-SECRET")) || segments(t, src)[0] != 0xE1 {
		t.Fatal("test input must carry the EXIF segment")
	}
	f, rc := saveAndOpen(t, s, pool, owner, src)
	out, _ := io.ReadAll(rc)
	rc.Close()
	for _, m := range segments(t, out) {
		if m == 0xE1 {
			t.Fatal("output still has an APP1 segment")
		}
	}
	if bytes.Contains(out, []byte("Exif")) || bytes.Contains(out, []byte("GPS-SECRET")) {
		t.Fatal("output still contains EXIF/GPS data")
	}
	if f.Width != 64 || f.Height != 48 {
		t.Fatalf("size %dx%d", f.Width, f.Height)
	}
}

func saveAndOpen(t *testing.T, s *Store, q db.Querier, owner uuid.UUID, data []byte) (File, io.ReadCloser) {
	t.Helper()
	ctx := context.Background()
	f, err := s.SaveImage(ctx, q, owner, PurposeIDFront, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}
	_, rc, err := s.Open(ctx, q, f.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return f, rc
}

func TestSaveImageAppliesOrientation(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()

	// 40×20: left half red, right half blue. Orientation 6 means "rotate 90°
	// clockwise to display", so the stored image is 20×40 with red on top.
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 20 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.Set(x, y, c)
		}
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		data := withEXIF(encodeJPEG(t, src), exifSegment(order, 6))
		f, err := s.SaveImage(ctx, pool, owner, PurposeVehiclePhoto, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if f.Width != 20 || f.Height != 40 {
			t.Fatalf("%v: rotated size %dx%d, want 20x40", order, f.Width, f.Height)
		}
		_, rc, err := s.Open(ctx, pool, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		top := color.RGBAModel.Convert(img.At(10, 5)).(color.RGBA)
		bottom := color.RGBAModel.Convert(img.At(10, 35)).(color.RGBA)
		if top.R < 200 || top.B > 60 || bottom.B < 200 || bottom.R > 60 {
			t.Fatalf("%v: wrong rotation: top=%v bottom=%v", order, top, bottom)
		}
	}
}

func TestSaveImageResizes(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	tests := []struct{ w, h, ww, wh int }{
		{2400, 1200, 2000, 1000},
		{900, 2250, 800, 2000},
		{2000, 900, 2000, 900}, // already within the limit: unchanged
	}
	for _, tc := range tests {
		f, err := s.SaveImage(context.Background(), pool, owner, PurposeVehiclePhoto, bytes.NewReader(encodePNG(t, gradient(tc.w, tc.h))))
		if err != nil {
			t.Fatal(err)
		}
		if f.Width != tc.ww || f.Height != tc.wh {
			t.Errorf("%dx%d -> %dx%d, want %dx%d", tc.w, tc.h, f.Width, f.Height, tc.ww, tc.wh)
		}
	}
}

func TestSaveImageFlattensTransparency(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()

	transparent := image.NewNRGBA(image.Rect(0, 0, 16, 16)) // all pixels fully transparent
	f, err := s.SaveImage(ctx, pool, owner, PurposeTopupReceipt, bytes.NewReader(encodePNG(t, transparent)))
	if err != nil {
		t.Fatal(err)
	}
	_, rc, err := s.Open(ctx, pool, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	img, err := jpeg.Decode(rc)
	if err != nil {
		t.Fatal(err)
	}
	c := color.RGBAModel.Convert(img.At(8, 8)).(color.RGBA)
	if c.R < 245 || c.G < 245 || c.B < 245 {
		t.Fatalf("transparent pixels must become white, got %v", c)
	}
}

func TestSaveImageWebP(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	// 1×1 lossless and lossy WebP files (the classic feature-detection images).
	for _, b64 := range []string{
		"UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==",
		"UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA",
	} {
		data, _ := base64.StdEncoding.DecodeString(b64)
		f, err := s.SaveImage(context.Background(), pool, owner, PurposeIDFront, bytes.NewReader(data))
		if err != nil {
			t.Fatalf("webp: %v", err)
		}
		if f.Width != 1 || f.Height != 1 {
			t.Fatalf("webp size %dx%d", f.Width, f.Height)
		}
	}
}

func TestSaveImageRejects(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	ctx := context.Background()
	s := newStore(t)

	noise := image.NewRGBA(image.Rect(0, 0, 100, 100))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range noise.Pix {
		noise.Pix[i] = uint8(rng.UintN(256))
	}
	bigPNG := encodePNG(t, noise)

	huge := encodePNG(t, gradient(1, 1)) // patch IHDR to claim 8000×6000 = 48 MP
	binary.BigEndian.PutUint32(huge[16:20], 8000)
	binary.BigEndian.PutUint32(huge[20:24], 6000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))

	truncated := encodePNG(t, gradient(50, 50))[:60]

	tests := []struct {
		name   string
		store  *Store
		data   []byte
		status int
		code   string
	}{
		{"pdf", s, []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"), 422, "unsupported_file_type"},
		{"plain text", s, []byte("hello, this is not an image"), 422, "unsupported_file_type"},
		{"svg", s, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 422, "unsupported_file_type"},
		{"empty", s, nil, 422, "unsupported_file_type"},
		{"truncated png", s, truncated, 422, "unsupported_file_type"},
		{"over 40 megapixels", s, huge, 422, "image_too_large"},
		{"over max bytes", &Store{Dir: s.Dir, MaxBytes: 1024}, bigPNG, 413, "file_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.store.SaveImage(ctx, pool, owner, PurposeIDBack, bytes.NewReader(tc.data))
			wantHTTPError(t, err, tc.status, tc.code)
		})
	}
	if _, err := s.SaveImage(ctx, pool, owner, "passport_scan", bytes.NewReader(bigPNG)); err == nil {
		t.Fatal("unknown purpose must fail")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM files`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected uploads must not create rows: %d %v", n, err)
	}
}

func TestOpenServeAndSoftDelete(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()

	f, err := s.SaveImage(ctx, pool, owner, PurposeIDFront, bytes.NewReader(encodeJPEG(t, gradient(32, 32))))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, pool, f.ID)
	if err != nil || got.SHA256 != f.SHA256 || got.OwnerUserID != owner || got.SizeBytes != f.SizeBytes {
		t.Fatalf("Get: %+v %v", got, err)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		meta, rc, err := s.Open(ctx, pool, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		ServeImage(rec, httptest.NewRequest(method, "/x", nil), meta, rc)
		h := rec.Result().Header
		checks := map[string]string{
			"Content-Type":           "image/jpeg",
			"Cache-Control":          "private, no-store",
			"X-Content-Type-Options": "nosniff",
			"Content-Disposition":    `inline; filename="` + f.ID.String() + `.jpg"`,
		}
		for k, v := range checks {
			if h.Get(k) != v {
				t.Errorf("%s %s = %q, want %q", method, k, h.Get(k), v)
			}
		}
		if method == http.MethodGet {
			sum := sha256.Sum256(rec.Body.Bytes())
			if hex.EncodeToString(sum[:]) != f.SHA256 {
				t.Error("served bytes differ from stored bytes")
			}
		} else if rec.Body.Len() != 0 {
			t.Error("HEAD must not send a body")
		}
	}

	if err := s.SoftDelete(ctx, pool, f.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Open(ctx, pool, f.ID)
	wantHTTPError(t, err, 404, "not_found")
	wantHTTPError(t, s.SoftDelete(ctx, pool, f.ID), 404, "not_found")
	_, _, err = s.Open(ctx, pool, uuid.New())
	wantHTTPError(t, err, 404, "not_found")
}

func TestOpenRejectsUnsafeKeys(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()

	// A secret outside the storage root.
	outside := t.TempDir()
	secretID := uuid.New()
	if err := os.WriteFile(filepath.Join(outside, secretID.String()+".jpg"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A key-shaped path whose month directory is a symlink leaving the root.
	if err := os.MkdirAll(filepath.Join(s.Dir, "2030"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Dir, "2030", "01")); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"../../../../../../etc/passwd",
		"/etc/passwd",
		"2030/01/../../../" + secretID.String() + ".jpg",
		"2030/01/" + secretID.String() + ".jpg", // well-formed, but escapes through the symlink
	} {
		id := uuid.New()
		_, err := pool.Exec(ctx, `
			INSERT INTO files (id, owner_user_id, purpose, storage_key, mime, size_bytes, sha256, width, height)
			VALUES ($1, $2, 'driver_avatar', $3, 'image/jpeg', 6, 'x', 1, 1)`, id, owner, key)
		if err != nil {
			t.Fatal(err)
		}
		_, rc, err := s.Open(ctx, pool, id)
		if err == nil {
			b, _ := io.ReadAll(rc)
			rc.Close()
			t.Fatalf("key %q was opened (read %q)", key, b)
		}
		wantHTTPError(t, err, 500, "internal")
	}
}

func TestRemoveOrphans(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()

	kept, err := s.SaveImage(ctx, pool, owner, PurposeDriverAvatar, bytes.NewReader(encodePNG(t, gradient(8, 8))))
	if err != nil {
		t.Fatal(err)
	}
	// An image written by a transaction that rolled back.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := s.SaveImage(ctx, tx, owner, PurposeDriverAvatar, bytes.NewReader(encodePNG(t, gradient(8, 8))))
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)

	old := time.Now().Add(-48 * time.Hour)
	var paths []string
	_ = filepath.Walk(s.Dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	stale := filepath.Join(filepath.Dir(paths[0]), tmpPrefix+"interrupted")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range append(paths, stale) {
		_ = os.Chtimes(p, old, old)
	}

	removed, err := s.RemoveOrphans(ctx, pool, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed %d files, want 2 (orphan + temp)", removed)
	}
	if _, rc, err := s.Open(ctx, pool, kept.ID); err != nil {
		t.Fatalf("tracked file must survive: %v", err)
	} else {
		rc.Close()
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale temp file must be removed")
	}
	_ = orphan
}

func TestJPEGOrientationParsing(t *testing.T) {
	base := encodeJPEG(t, gradient(4, 4))
	for o := uint16(1); o <= 8; o++ {
		for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			if got := jpegOrientation(withEXIF(base, exifSegment(order, o))); got != int(o) {
				t.Errorf("orientation %d (%v): got %d", o, order, got)
			}
		}
	}
	if jpegOrientation(base) != 1 || jpegOrientation(withEXIF(base, exifSegment(binary.BigEndian, 42))) != 1 {
		t.Error("missing or out-of-range orientation must be 1")
	}
	// Truncated or corrupt segments must not panic.
	seg := exifSegment(binary.LittleEndian, 6)
	for cut := 0; cut < len(seg); cut++ {
		_ = jpegOrientation(append(append([]byte{0xFF, 0xD8}, seg[:cut]...), 0xFF, 0xD9))
	}
}

func FuzzJPEGOrientation(f *testing.F) {
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x02})
	f.Add(withEXIF([]byte{0xFF, 0xD8, 0xFF, 0xD9}, exifSegment(binary.BigEndian, 3)))
	f.Fuzz(func(t *testing.T, b []byte) {
		if o := jpegOrientation(b); o < 1 || o > 8 {
			t.Fatalf("orientation %d out of range", o)
		}
	})
}

func TestNewCreatesPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	s, err := New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(s.Dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v %v", st, err)
	}
	if s.maxBytes() != DefaultMaxBytes {
		t.Fatal("default max bytes")
	}
	if _, err := New("", 0); err == nil {
		t.Fatal("empty dir must fail")
	}
}
