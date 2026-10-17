package receipt_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt/receipttest"
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

// TestCalibrationSamples parses real Kaspi receipts kept outside the
// repository (JT_KASPI_RECEIPT_SAMPLES=<dir>). It logs no personal data.
func TestCalibrationSamples(t *testing.T) {
	dir := os.Getenv("JT_KASPI_RECEIPT_SAMPLES")
	if dir == "" {
		t.Skip("set JT_KASPI_RECEIPT_SAMPLES to a directory of real Kaspi receipt PDFs")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.pdf"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no samples in %s (%v)", dir, err)
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
