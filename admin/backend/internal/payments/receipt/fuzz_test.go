package receipt_test

import (
	"context"
	"testing"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt"
	"github.com/yereke99/jantaxi/admin/backend/internal/payments/receipt/receipttest"
)

// FuzzParse feeds arbitrary bytes (seeded with synthetic receipts and broken
// variants) to Parse: it must return quickly without panicking.
// Run: go test -run '^$' -fuzz FuzzParse -fuzztime 60s ./admin/backend/internal/payments/receipt
func FuzzParse(f *testing.F) {
	s := receipttest.Default()
	good := receipttest.Build(s)
	edited := s
	edited.Amount = "9 000"
	qr := s
	qr.QRPayload = "QR18149336524"
	f.Add(good)
	f.Add(receipttest.BuildEdited(s, edited))
	f.Add(receipttest.BuildSpliced(s, edited))
	f.Add(receipttest.Build(qr))
	f.Add(good[:len(good)/2])
	f.Add([]byte("%PDF-1.7\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj 2 0 obj <</Type /Pages /Kids [2 0 R 3 0 R]>> endobj 3 0 obj <</Type /Page /Contents 4 0 R /Parent 2 0 R>> endobj 4 0 obj <</Length 12>> stream\nBT (x) Tj ET\nendstream endobj trailer <</Root 1 0 R>> %%EOF"))
	f.Add([]byte("%PDF-1.7\n1 0 obj [[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[ endobj"))
	f.Add([]byte("%PDF-1.7\n1 0 obj <</Type /ObjStm /N 99999 /First 0 /Filter /FlateDecode /Length 3>> stream\nabc\nendstream endobj"))
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		r := receipt.Parse(context.Background(), data, receipt.Options{FileName: "receipt_QR18149336524.pdf"})
		if r.ParseStatus == "" || len(r.FileSHA256) != 64 {
			t.Fatalf("incomplete result %+v", r)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("parse took %v", d)
		}
	})
}
