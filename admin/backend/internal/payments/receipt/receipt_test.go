package receipt_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt/receipttest"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/pdfdoc"
)

var paidAt = time.Date(2026, 10, 17, 13, 55, 0, 0, receipttest.Astana)

func parse(t *testing.T, pdf []byte, name string) receipt.Receipt {
	t.Helper()
	return receipt.Parse(context.Background(), pdf, receipt.Options{FileName: name})
}

func TestParseKaspiReceipt(t *testing.T) {
	s := receipttest.Default()
	s.Amount, s.PaidAt = "5 000", paidAt
	r := parse(t, receipttest.Build(s), "receipt_QR18149336524.pdf")
	if r.ParseStatus != receipt.StatusParsed || len(r.Missing) != 0 {
		t.Fatalf("status %s missing %v flags %v", r.ParseStatus, r.Missing, r.Flags)
	}
	if r.Serial != "QR18149336524" || r.Amount != 5000 || r.RecipientBIN != "000809500289" ||
		r.RecipientName != `ИП "JAN-TAXI"` || r.PaymentMethod != "с Kaspi Gold" || r.Producer != "WeasyPrint 70.0" {
		t.Fatalf("fields: %+v", r)
	}
	if r.PaidAt == nil || !r.PaidAt.Equal(paidAt) || r.PaidAt.Location() != time.UTC {
		t.Fatalf("paid_at = %v, want %v", r.PaidAt, paidAt)
	}
	if r.PayerMasked != "Т*** Т." {
		t.Fatalf("payer = %q", r.PayerMasked)
	}
	if !slices.Equal(r.Flags, []string{receipt.FlagQRNotFound}) || len(r.Blocking()) != 0 {
		t.Fatalf("flags = %v", r.Flags)
	}
	if len(r.FileSHA256) != 64 || r.QRPayloadHash != "" {
		t.Fatalf("hashes %q %q", r.FileSHA256, r.QRPayloadHash)
	}
}

func TestParseFlags(t *testing.T) {
	base := receipttest.Default()
	base.PaidAt = paidAt
	edited := base
	edited.Amount = "50 000"
	tests := []struct {
		name     string
		pdf      []byte
		file     string
		want     []string // flags that must be raised
		blocking bool
	}{
		{"incremental update", receipttest.BuildEdited(base, edited), "", []string{receipt.FlagIncrementalUpdate, receipt.FlagObjectRedefined}, true},
		{"trailing data", append(receipttest.Build(base), []byte("\n5 0 obj (x) endobj\n")...), "", []string{receipt.FlagTrailingData}, true},
		{"other producer", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Producer = "Microsoft: Print To PDF" })), "", []string{receipt.FlagProducerUnexpected}, true},
		{"no producer", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Producer = "-" })), "", []string{receipt.FlagProducerUnexpected}, true},
		{"creator", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Creator = "Adobe Acrobat Pro" })), "", []string{receipt.FlagCreatorPresent}, true},
		{"moddate", receipttest.Build(with(base, func(s *receipttest.Spec) { s.ModDate = true })), "", []string{receipt.FlagModDatePresent}, true},
		{"other font", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Font = "ArialMT" })), "", []string{receipt.FlagFontUnexpected}, true},
		{"annotation", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Annotation = true })), "", []string{receipt.FlagAnnotations}, true},
		{"serial without label", receipttest.Build(with(base, func(s *receipttest.Spec) { s.NoSerialLabel = true })), "", []string{receipt.FlagSerialLabelMissing}, true},
		{"serial format", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Serial = "AB12345678" })), "", []string{receipt.FlagSerialFormat}, true},
		{"fractional amount", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Amount = "5 000,50" })), "", []string{receipt.FlagAmountFractional}, true},
		{"not paid", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Status = "Ожидает оплаты" })), "", []string{receipt.FlagStatusMissing}, true},
		{"file name", receipttest.Build(base), "receipt_QR99999999999.pdf", []string{receipt.FlagFileNameMismatch}, true},
		{"qr names other receipt", receipttest.Build(with(base, func(s *receipttest.Spec) { s.QRPayload = "https://kaspi.kz/receipt?id=QR11111111111" })), "", []string{receipt.FlagQRMismatch}, true},
		{"no bin line", receipttest.Build(with(base, func(s *receipttest.Spec) { s.NoBINLine = true })), "", []string{receipt.FlagBINMissing}, false},
		{"kazakh status", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Status = "Төлем жасалды" })), "", []string{receipt.FlagStatusKazakh}, false},
		{"edited in place", receipttest.BuildSpliced(base, edited), "", []string{receipt.FlagXRefInconsistent}, true},
		{"rewritten by qpdf", receipttest.Build(with(base, func(s *receipttest.Spec) { s.Header = "%\xbf\xf7\xa2\xfe" })), "", []string{receipt.FlagHeaderUnexpected}, true},
		{"no end marker", bytes.TrimSuffix(receipttest.Build(base), []byte("%%EOF\n")), "", []string{receipt.FlagStructure}, true},
		{"stale startxref", bytes.Replace(receipttest.Build(base), []byte("startxref\n"), []byte("startxref\n1"), 1), "", []string{receipt.FlagXRefInconsistent}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := parse(t, tt.pdf, tt.file)
			for _, f := range tt.want {
				if !r.HasFlag(f) {
					t.Errorf("flag %s missing: %v", f, r.Flags)
				}
			}
			if got := len(r.Blocking()) > 0; got != tt.blocking {
				t.Errorf("blocking = %v (%v), want %v", got, r.Blocking(), tt.blocking)
			}
		})
	}
}

func TestParseEditedInPlaceReadsTheEditedContent(t *testing.T) {
	base := receipttest.Default()
	edited := base
	edited.Amount = "5 000"
	r := parse(t, receipttest.BuildSpliced(base, edited), "")
	if r.Amount != 5000 || r.ParseStatus != receipt.StatusParsed {
		t.Fatalf("amount = %d status %s", r.Amount, r.ParseStatus)
	}
	// Only the stale offsets give this edit away.
	if got := r.Blocking(); !slices.Equal(got, []string{receipt.FlagXRefInconsistent}) {
		t.Fatalf("blocking = %v", got)
	}
}

func TestParseEditedReadsTheVisibleRevision(t *testing.T) {
	base := receipttest.Default()
	edited := base
	edited.Amount = "50 000"
	r := parse(t, receipttest.BuildEdited(base, edited), "")
	if r.Amount != 50000 {
		t.Fatalf("amount = %d, want the edited (displayed) amount", r.Amount)
	}
}

func TestParseQRMatchingSerial(t *testing.T) {
	s := receipttest.Default()
	s.QRPayload = "https://kaspi.kz/check?receipt=QR18149336524"
	r := parse(t, receipttest.Build(s), "")
	if r.QRPayloadHash == "" || r.HasFlag(receipt.FlagQRNotFound) || r.HasFlag(receipt.FlagQRMismatch) || len(r.Blocking()) != 0 {
		t.Fatalf("qr: hash %q flags %v", r.QRPayloadHash, r.Flags)
	}
	other := s
	other.QRPayload = "https://kaspi.kz/check?receipt=QR18149336524&n=2"
	if r2 := parse(t, receipttest.Build(other), ""); r2.QRPayloadHash == r.QRPayloadHash {
		t.Fatal("different payloads must hash differently")
	}
}

func TestParseRejectsNonPDF(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("hello"), []byte("%PDF-1.7\n"), bytes.Repeat([]byte("%PDF-1.4 1 0 obj << /Kids [1 0 R] >> endobj "), 50)} {
		r := parse(t, data, "")
		if r.ParseStatus != receipt.StatusUnreadable && r.ParseStatus != receipt.StatusPartial {
			t.Fatalf("status %s for %q", r.ParseStatus, data)
		}
		if len(r.Blocking()) == 0 && r.ParseStatus != receipt.StatusPartial {
			t.Fatalf("unreadable file without blocking flags: %v", r.Flags)
		}
	}
}

func TestMaskName(t *testing.T) {
	tests := map[string]string{
		"Ерлан Б.":             "Е*** Б.",
		"Иванов Иван Иванович": "И*** И*** И***",
		"Ли":                   "Л***",
		"А Б В Г":              "А Б В",
	}
	for in, want := range tests {
		if got := receipt.MaskName(in); got != want {
			t.Errorf("MaskName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSellerMatches(t *testing.T) {
	tests := []struct {
		printed, expected string
		want              bool
	}{
		{`ИП "JAN-TAXI"`, `ИП "JAN-TAXI"`, true},
		{`ИП «JAN TAXI»`, `ИП "JAN-TAXI"`, true},
		{`ИП "JАN-TАXI"`, `ИП "JAN-TAXI"`, true}, // Cyrillic А in the printed name
		{`ТОО "JAN-TAXI KZ"`, `JAN-TAXI`, true},
		{`ИП "TAXI JAN"`, `ИП "JAN-TAXI"`, false},
		{`ИП "Иванов"`, `ИП "JAN-TAXI"`, false},
		{`ИП "JAN-TAXI"`, `ИП`, false}, // nothing distinctive configured
	}
	for _, tt := range tests {
		if got := receipt.SellerMatches(tt.printed, tt.expected); got != tt.want {
			t.Errorf("SellerMatches(%q, %q) = %v", tt.printed, tt.expected, got)
		}
	}
}

func with(s receipttest.Spec, f func(*receipttest.Spec)) receipttest.Spec {
	f(&s)
	return s
}

// samplesDir is the directory of real Kaspi receipts: JT_KASPI_RECEIPT_SAMPLES,
// else the private folder next to the repository. Real receipts carry
// personal data and are never committed.
func samplesDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("JT_KASPI_RECEIPT_SAMPLES")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "..", "..", "..", "jan-taxi-private", "kaspi-receipt-samples")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Skipf("no real Kaspi receipts in %s (set JT_KASPI_RECEIPT_SAMPLES)", dir)
	}
	return dir
}

// TestRealKaspiReceipt parses the calibration receipt (local only).
func TestRealKaspiReceipt(t *testing.T) {
	f := filepath.Join(samplesDir(t), "receipt_QR18149336524.pdf")
	data, err := os.ReadFile(f)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is not present", filepath.Base(f))
	}
	if err != nil {
		t.Fatal(err)
	}
	r := parse(t, data, filepath.Base(f))
	if r.ParseStatus != receipt.StatusParsed || len(r.Missing) != 0 || len(r.Blocking()) != 0 {
		t.Fatalf("status %s missing %v blocking %v", r.ParseStatus, r.Missing, r.Blocking())
	}
	if r.Serial != "QR18149336524" || r.Amount != 500 || r.RecipientBIN != "000809500289" {
		t.Fatalf("serial %s amount %d bin %s", r.Serial, r.Amount, r.RecipientBIN)
	}
	if !receipt.SellerMatches(r.RecipientName, `ИП "JAN-TAXI"`) || !strings.HasPrefix(r.Producer, "WeasyPrint") {
		t.Fatalf("seller or producer not recognised (producer %q)", r.Producer)
	}
	want := time.Date(2026, 10, 17, 13, 55, 0, 0, receipt.Astana)
	if r.PaidAt == nil || !r.PaidAt.Equal(want) {
		t.Fatalf("paid_at = %v, want %v", r.PaidAt, want)
	}
	if !strings.Contains(r.PayerMasked, "***") || r.PaymentMethod == "" {
		t.Fatal("payer is not masked or the payment method is missing")
	}
	if !slices.Equal(r.Flags, []string{receipt.FlagQRNotFound}) {
		t.Fatalf("flags = %v", r.Flags)
	}
}

// TestCalibrationSamples parses every real Kaspi receipt in the samples
// directory (local only). It logs no personal data.
func TestCalibrationSamples(t *testing.T) {
	dir := samplesDir(t)
	files, err := filepath.Glob(filepath.Join(dir, "*.pdf"))
	if err != nil || len(files) == 0 {
		t.Skipf("no samples in %s (%v)", dir, err)
	}
	serialRe := regexp.MustCompile(`^QR[0-9]{8,}$`)
	nameRe := regexp.MustCompile(`(?i)QR[0-9]{8,}`)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		r := parse(t, data, filepath.Base(f))
		t.Logf("%s: status=%s serial=%s amount=%d bin=%s paid_at=%v producer=%q flags=%v",
			filepath.Base(f), r.ParseStatus, r.Serial, r.Amount, r.RecipientBIN, r.PaidAt, r.Producer, r.Flags)
		if r.ParseStatus != receipt.StatusParsed || !serialRe.MatchString(r.Serial) || r.Amount <= 0 ||
			r.PaidAt == nil || len(r.Blocking()) != 0 || r.PayerMasked == "" || !strings.Contains(r.PayerMasked, "***") {
			t.Errorf("%s: not read as a clean Kaspi receipt (missing %v, blocking %v)", filepath.Base(f), r.Missing, r.Blocking())
		}
		if m := nameRe.FindString(filepath.Base(f)); m != "" && strings.ToUpper(m) != r.Serial {
			t.Errorf("%s: serial %s differs from the file name", filepath.Base(f), r.Serial)
		}
	}
}

// TestSyntheticReceiptMatchesKaspiStructure pins the calibrated structure of
// real Kaspi receipts (ARCHITECTURE §6.1) on the synthetic ones the tests use.
func TestSyntheticReceiptMatchesKaspiStructure(t *testing.T) {
	data := receipttest.Build(receipttest.Default())
	checkKaspiStructure(t, data)
}

func checkKaspiStructure(t *testing.T, data []byte) {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("%PDF-1.7\n%\xf0\x9f\x96\xa4\n")) || !bytes.HasSuffix(data, []byte("%%EOF\n")) {
		t.Fatal("header or end marker differs from WeasyPrint's")
	}
	doc, err := pdfdoc.Open(data, pdfdoc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if doc.EOFMarkers != 1 || doc.StartXRefs != 1 || doc.XRefTables != 0 || doc.XRefStreams != 1 || doc.HasPrev ||
		doc.Redefined != 0 || doc.TrailingBytes != 0 || doc.XRefMismatches() != 0 {
		t.Fatalf("revision structure: eof %d startxref %d tables %d streams %d prev %v redefined %d trailing %d xref %d",
			doc.EOFMarkers, doc.StartXRefs, doc.XRefTables, doc.XRefStreams, doc.HasPrev, doc.Redefined, doc.TrailingBytes, doc.XRefMismatches())
	}
	if p, _ := doc.InfoString("Producer"); !strings.HasPrefix(p, "WeasyPrint") {
		t.Fatalf("producer %q", p)
	}
	for _, k := range []pdfdoc.Name{"Creator", "ModDate"} {
		if _, ok := doc.InfoString(k); ok {
			t.Fatalf("info has %s", k)
		}
	}
	if doc.HasXMPMetadata() || doc.HasForm() || len(doc.ActiveContent()) != 0 {
		t.Fatal("metadata, form or active content present")
	}
	pages := doc.Pages()
	if len(pages) != 1 || doc.Annotations(pages[0]) != 0 {
		t.Fatalf("pages %d", len(pages))
	}
	mb := pages[0].Dict["MediaBox"].(pdfdoc.Array)
	if w, _ := pdfdoc.Number(mb[2]); w != 360 {
		t.Fatalf("media box %v", mb)
	}
	text, err := doc.ExtractText(pages[0])
	if err != nil || text.Undecodable != 0 || !slices.Equal(text.Fonts, []string{"Roboto-Bold", "Roboto"}) || len(text.Runs) != 18 {
		t.Fatalf("text: err %v undecodable %d fonts %v runs %d", err, text.Undecodable, text.Fonts, len(text.Runs))
	}
	fonts := pages[0].Resources["Font"]
	if fd, ok := doc.Resolve(fonts).(pdfdoc.Dict); ok {
		for name, ref := range fd {
			f, _ := doc.Resolve(ref).(pdfdoc.Dict)
			if f.Name("Subtype") != "Type0" || f.Name("Encoding") != "Identity-H" || f["ToUnicode"] == nil {
				t.Fatalf("font %s is not Type0 Identity-H with ToUnicode: %v", name, f)
			}
		}
	} else {
		t.Fatal("no font resources")
	}
}

// layoutOf is the text layout of a receipt without its values: labels as
// printed, every other run as its font and size.
func layoutOf(t *testing.T, data []byte) []string {
	t.Helper()
	labels := map[string]bool{"Оплата совершена": true, "₸": true, "№ чека": true, "ИИН/БИН продавца": true,
		"ФИО покупателя": true, "Дата и время": true, "по Астане": true, "Оплачено": true, "Адрес": true,
		"Чек не является фискальным": true}
	doc, err := pdfdoc.Open(data, pdfdoc.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	text, err := doc.ExtractText(doc.Pages()[0])
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range text.Runs {
		if s := strings.TrimSpace(r.Text); labels[s] {
			out = append(out, s)
		} else {
			out = append(out, fmt.Sprintf("<%s %g>", r.Font, r.FontSize))
		}
	}
	return out
}

// TestSyntheticReceiptMatchesRealLayout compares the synthetic receipt with
// the real one (local only): same structure, same labels in the same order,
// values in the same fonts and sizes.
func TestSyntheticReceiptMatchesRealLayout(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(samplesDir(t), "receipt_QR18149336524.pdf"))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("calibration receipt is not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	checkKaspiStructure(t, data)
	original, synthetic := layoutOf(t, data), layoutOf(t, receipttest.Build(receipttest.Default()))
	if !slices.Equal(original, synthetic) {
		t.Fatalf("layouts differ:\nreal      %v\nsynthetic %v", original, synthetic)
	}
}
