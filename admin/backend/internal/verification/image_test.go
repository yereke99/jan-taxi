package verification

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/draw"
)

func TestCardRegionsAndPreparation(t *testing.T) {
	page := image.NewRGBA(image.Rect(0, 0, 736, 1600))
	draw.Draw(page, page.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	want := []image.Rectangle{image.Rect(56, 370, 680, 770), image.Rect(56, 785, 680, 1180)}
	for _, r := range want {
		draw.Draw(page, r, image.NewUniform(color.RGBA{185, 225, 218, 255}), image.Point{}, draw.Src)
	}
	got := cardRegions(page)
	if len(got) != 2 {
		t.Fatalf("detected %d cards", len(got))
	}
	for i, r := range want {
		if !r.In(got[i]) {
			t.Fatal("card edge cropped away")
		}
	}
	p := filepath.Join(t.TempDir(), "source.png")
	var raw bytes.Buffer
	if err := png.Encode(&raw, page); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := prepareCards(context.Background(), p)
	if err != nil || len(paths) != 2 {
		t.Fatalf("prepare %d: %v", len(paths), err)
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width != 2020 || cfg.Height > 2020 {
			t.Fatal("unbounded or invalid crop")
		}
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0600 {
			t.Fatal("crop is not private")
		}
	}
	stored, _ := os.ReadFile(p)
	if !bytes.Equal(stored, raw.Bytes()) {
		t.Fatal("original changed")
	}
	gray := image.NewGray(image.Rect(0, 0, 300, 200))
	if len(cardRegions(gray)) != 0 {
		t.Fatal("grayscale page treated as coloured card")
	}
}

// Opt-in local benchmark. Private files are supplied outside the repository;
// only field presence/scores and validity are reported, never recognized values.
func TestAuthorizedOCRSample(t *testing.T) {
	sample := os.Getenv("JT_OCR_SAMPLE")
	if sample == "" {
		t.Skip("set JT_OCR_SAMPLE inside the OCR runtime")
	}
	b, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal("sample unavailable")
	}
	p := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := prepareCards(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("card_crops=%d", len(paths))
	if os.Getenv("JT_OCR_NO_CROP") == "1" {
		paths = nil
	}
	if len(paths) == 0 {
		paths = []string{p}
	}
	var lines []Line
	if script := os.Getenv("JT_OCR_RECOGNIZER"); script != "" {
		cards := map[string]bool{}
		for _, path := range paths {
			cards[path] = path != p
		}
		lines, err = (LocalEngine{Recognizer: script}).recognize(context.Background(), filepath.Dir(p), paths, cards)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		for _, p := range paths {
			b, err := run(context.Background(), filepath.Dir(p), "tesseract", p, "stdout", "-l", "kaz+rus+eng", "--oem", "1", "--psm", "11", "-c", "tessedit_create_tsv=1")
			if err != nil {
				t.Fatal(err)
			}
			found, err := ParseTSV(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			for i := range found {
				found[i].FromCard = p != filepath.Join(filepath.Dir(p), "source")
			}
			lines = append(lines, found...)
		}
	}
	r := Extract(lines)
	t.Logf("lines=%d type_found=%t first_found=%t last_found=%t valid_iin=%t valid_number=%t scores=%v", len(lines), r.DocumentType != "", r.FirstName != "", r.LastName != "", ValidIIN(r.Extracted.IIN), validDocumentNumber(r.DocumentType, r.Extracted.DocumentNumber), r.Fields)
	for i, l := range lines {
		field, _ := personalField(NormalizeName(l.Text))
		digits := iinRE.FindStringSubmatch(l.Text)
		if field == "iin" || len(digits) > 1 {
			t.Logf("numeric_line=%d labelled_iin=%t twelve_digits=%t score=%.2f", i, field == "iin", len(digits) > 1, l.Confidence)
		}
		if os.Getenv("JT_OCR_LAYOUT") == "1" {
			t.Logf("row=%d label=%s name_candidate=%t score=%.2f words=%d", i, field, nameValue(l.Text), l.Confidence, len(l.Words))
		}
	}
}
