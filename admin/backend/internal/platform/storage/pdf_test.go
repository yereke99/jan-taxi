package storage

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// minimalPDF is a one-page PDF with a text line.
var minimalPDF = []byte("%PDF-1.4\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj\n" +
	"2 0 obj <</Type /Pages /Kids [3 0 R] /Count 1>> endobj\n" +
	"3 0 obj <</Type /Page /Parent 2 0 R /Contents 4 0 R>> endobj\n" +
	"4 0 obj <</Length 18>> stream\nBT (receipt) Tj ET\nendstream endobj\n" +
	"trailer <</Root 1 0 R>>\nstartxref\n0\n%%EOF\n")

func TestSavePDF(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()

	f, err := s.SavePDF(ctx, pool, owner, PurposeTopupReceipt, minimalPDF)
	if err != nil {
		t.Fatal(err)
	}
	if f.Mime != MimePDF || f.Width != 0 || f.Height != 0 || f.SizeBytes != int64(len(minimalPDF)) || len(f.SHA256) != 64 {
		t.Fatalf("file = %+v", f)
	}
	var key string
	if err := pool.QueryRow(ctx, `SELECT storage_key FROM files WHERE id = $1`, f.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(s.Dir, filepath.FromSlash(key)))
	if err != nil || st.Mode().Perm() != 0o600 || filepath.Ext(key) != ".pdf" {
		t.Fatalf("stored %s: %v %v", key, st, err)
	}
	got, rc, err := s.Open(ctx, pool, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	ServeFile(rec, httptest.NewRequest(http.MethodGet, "/x", nil), got, rc)
	h := rec.Header()
	if h.Get("Content-Type") != "application/pdf" || h.Get("Cache-Control") != "private, no-store" ||
		h.Get("Content-Disposition") != `inline; filename="`+f.ID.String()+`.pdf"` || h.Get("X-Content-Type-Options") != "nosniff" ||
		!bytes.Equal(rec.Body.Bytes(), minimalPDF) {
		t.Fatalf("served %v %q", h, rec.Body.Bytes())
	}
	// ServeImage hands PDFs to ServePDF too (existing file endpoints).
	got, rc, _ = s.Open(ctx, pool, f.ID)
	rec = httptest.NewRecorder()
	ServeImage(rec, httptest.NewRequest(http.MethodGet, "/x", nil), got, rc)
	if rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("ServeImage served a PDF as %s", rec.Header().Get("Content-Type"))
	}
	// Orphan cleanup keeps PDFs that have a row.
	if n, err := s.RemoveOrphans(ctx, pool, 0); err != nil || n != 0 {
		t.Fatalf("orphans removed %d (%v)", n, err)
	}
}

func TestIdentityPDFPageAndInspectionLimits(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.SavePDF(ctx, pool, owner, PurposeIDFront, minimalPDF); err != nil {
		t.Fatal("valid identity PDF rejected", err)
	}
	three := bytes.ReplaceAll(minimalPDF, []byte("/Kids [3 0 R] /Count 1"), []byte("/Kids [3 0 R 5 0 R 6 0 R] /Count 3"))
	three = bytes.Replace(three, []byte("trailer"), []byte("5 0 obj <</Type /Page /Parent 2 0 R>> endobj\n6 0 obj <</Type /Page /Parent 2 0 R>> endobj\ntrailer"), 1)
	_, err := s.SavePDF(ctx, pool, owner, PurposeIDFront, three)
	wantHTTPError(t, err, 422, "pdf_page_limit")
	deep := bytes.Replace(minimalPDF, []byte("/Type /Catalog /Pages"), []byte("/Nested "+strings.Repeat("<</Nested ", 40)+"null"+strings.Repeat(">>", 40)+" /Type /Catalog /Pages"), 1)
	_, err = s.SavePDF(ctx, pool, owner, PurposeIDBack, deep)
	wantHTTPError(t, err, 422, "pdf_unreadable")
}

func TestSavePDFRejects(t *testing.T) {
	pool := testutil.NewDB(t)
	owner, _ := testutil.CreateUser(t, pool)
	s := newStore(t)
	ctx := context.Background()
	js := []byte("%PDF-1.7\n1 0 obj <</Type /Catalog /Pages 2 0 R /Names <</JavaScript 3 0 R>>>> endobj\n2 0 obj <</Type /Pages /Kids []>> endobj\n3 0 obj <</Names [(a) 4 0 R]>> endobj\n4 0 obj <</S /JavaScript /JS (app.alert(1))>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF")
	enc := append(append([]byte(nil), minimalPDF...), []byte("trailer <</Root 1 0 R /Encrypt <</Filter /Standard>>>>\n%%EOF\n")...)
	attach := []byte("%PDF-1.7\n1 0 obj <</Type /Catalog /Pages 2 0 R /Names <</EmbeddedFiles 3 0 R>>>> endobj\n2 0 obj <</Type /Pages /Kids []>> endobj\n3 0 obj <</Names []>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF")
	tests := []struct {
		name   string
		data   []byte
		status int
		code   string
	}{
		{"empty", nil, 422, "unsupported_file_type"},
		{"not a pdf", []byte("GIF89a...."), 422, "unsupported_file_type"},
		{"broken", []byte("%PDF-1.7 garbage"), 422, "pdf_unreadable"},
		{"javascript", js, 422, "pdf_not_allowed"},
		{"encrypted", enc, 422, "pdf_not_allowed"},
		{"attachment", attach, 422, "pdf_not_allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.SavePDF(ctx, pool, owner, PurposeTopupReceipt, tt.data)
			wantHTTPError(t, err, tt.status, tt.code)
		})
	}
	small := &Store{Dir: s.Dir, MaxBytes: DefaultMaxBytes, MaxPDFBytes: 64}
	_, err := small.SavePDF(ctx, pool, owner, PurposeTopupReceipt, minimalPDF)
	wantHTTPError(t, err, 413, "file_too_large")
	if _, err := s.SavePDF(ctx, pool, owner, PurposeDriverAvatar, minimalPDF); err == nil {
		t.Fatal("a PDF avatar must be refused")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM files`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("files rows = %d (%v)", n, err)
	}
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if e.IsDir() {
			sub, _ := os.ReadDir(filepath.Join(s.Dir, e.Name()))
			if len(sub) > 0 {
				t.Fatalf("files written for refused uploads: %v", sub)
			}
		}
	}
}
