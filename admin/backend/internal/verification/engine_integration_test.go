package verification

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/logging"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/storage"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Simple OCR test sheets, without portraits, emblems or security elements.
// Personal fields are synthetic. The image is generated only inside temp dirs.
func ocrTestSheet(t *testing.T) image.Image {
	t.Helper()
	data, err := os.ReadFile("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf")
	if err != nil {
		t.Fatal("test font unavailable")
	}
	f, err := opentype.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 42, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	im := image.NewRGBA(image.Rect(0, 0, 1000, 1250))
	draw.Draw(im, im.Bounds(), image.White, image.Point{}, draw.Src)
	for _, r := range []image.Rectangle{image.Rect(20, 20, 980, 620), image.Rect(20, 640, 980, 1240)} {
		draw.Draw(im, r, image.NewUniform(color.RGBA{190, 230, 220, 255}), image.Point{}, draw.Src)
	}
	d := font.Drawer{Dst: im, Src: image.Black, Face: face}
	text := func(x, y int, s string) { d.Dot = fixed.P(x, y); d.DrawString(s) }
	text(60, 80, "TEST ONLY — OCR FIXTURE")
	text(60, 135, "УДОСТОВЕРЕНИЕ ЛИЧНОСТИ")
	text(360, 220, "ФАМИЛИЯ")
	text(360, 270, "ИВАНОВ")
	text(360, 350, "ИМЯ")
	text(360, 400, "АЛЕКСАНДР")
	text(60, 560, "ИИН "+ocrSheetInput().IIN)
	text(60, 720, "TEST ONLY — REVERSE")
	text(600, 790, ocrSheetInput().DocumentNumber)
	return im
}

func ocrSheetInput() Input {
	for digit := 0; digit < 10; digit++ {
		v := Input{DocumentType: "id_card", IIN: fmt.Sprintf("12345678901%d", digit), DocumentNumber: "123456789"}
		if ValidIIN(v.IIN) {
			return v
		}
	}
	panic("invalid synthetic IIN fixture")
}

func sheetPDF(t *testing.T, im image.Image) []byte {
	t.Helper()
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, im, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	stream := "q 1000 0 0 1250 0 0 cm /Im0 Do Q"
	objects := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1000 1250] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>"),
		append(append([]byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 1000 /Height 1250 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", jpg.Len())), jpg.Bytes()...), []byte("\nendstream")...),
		[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := []int{0}
	for i, o := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n", i+1)
		b.Write(o)
		b.WriteString("\nendobj\n")
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}

func TestLocalEngineCombinedSidesAndDuplicateSlots(t *testing.T) {
	if os.Getenv("JT_OCR_INTEGRATION") != "1" {
		t.Skip("requires the bounded OCR container and isolated PostGIS")
	}
	for _, format := range []string{"png", "pdf"} {
		t.Run(format, func(t *testing.T) {
			ctx := context.Background()
			s := testService(t)
			s.pool = testutil.NewDB(t)
			var logs bytes.Buffer
			s.opt.Log = logging.NewWriter(&logs, "info", "json")
			store, err := storage.New(t.TempDir(), 10<<20)
			if err != nil {
				t.Fatal(err)
			}
			d := testutil.CreateDriver(t, s.pool, testutil.DriverOptions{Status: "pending"})
			im := ocrTestSheet(t)
			var imageBytes bytes.Buffer
			if err := png.Encode(&imageBytes, im); err != nil {
				t.Fatal(err)
			}
			var docs []Document
			for _, kind := range []string{storage.PurposeIDFront, storage.PurposeIDBack} {
				var file storage.File
				if format == "pdf" {
					file, err = store.SavePDF(ctx, s.pool, d.UserID, kind, sheetPDF(t, im))
				} else {
					file, err = store.SaveImage(ctx, s.pool, d.UserID, kind, bytes.NewReader(imageBytes.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				docs = append(docs, Document{ID: file.ID, SHA256: file.SHA256, Kind: kind})
			}
			var app uuid.UUID
			err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
				if err := s.SaveInput(ctx, tx, d.DriverID, ocrSheetInput()); err != nil {
					return err
				}
				if err := tx.QueryRow(ctx, `INSERT INTO driver_applications(driver_id,round,status,snapshot) VALUES($1,1,'pending','{}') RETURNING id`, d.DriverID).Scan(&app); err != nil {
					return err
				}
				return s.Enqueue(ctx, tx, d.DriverID, app, "АЛЕКСАНДР", "ИВАНОВ", docs)
			})
			if err != nil {
				t.Fatal(err)
			}
			engine := LocalEngine{Service: s, Store: store, Recognizer: "/opt/jantaxi/ocr/recognize.py"}
			if err := engine.Check(ctx); err != nil {
				t.Fatal(err)
			}
			approved := 0
			approve := func(ctx context.Context, tx pgx.Tx, driverID, appID uuid.UUID) error { approved++; return nil }
			start := time.Now()
			did, err := s.RunOnce(ctx, engine, approve, WorkerOptions{Timeout: 60 * time.Second, MaxAttempts: 1})
			if err != nil || !did {
				t.Fatal("processing failed", err)
			}
			summary, err := s.Summary(ctx, s.pool, d.DriverID, true)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Status != "approved" || approved != 1 {
				t.Fatalf("synthetic sheet: status=%s first=%q scores=%v reasons=%v assessment=%v", summary.Status, summary.Result.FirstName, summary.Result.Fields, summary.Reasons, summary.Result.Comparisons)
			}
			if !strings.Contains(logs.String(), `"duplicate_slots":1`) {
				t.Fatal("combined file processed twice")
			}
			for _, private := range []string{"АЛЕКСАНДР", "ИВАНОВ", ocrSheetInput().IIN, ocrSheetInput().DocumentNumber} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("private values logged")
				}
			}
			t.Logf("combined %s: one unique file, two sides, automatic approval; duration=%s", format, time.Since(start).Round(time.Millisecond))
		})
	}
}
