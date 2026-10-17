// Package receipt reads Kaspi payment receipts ("чек") shared by drivers as
// PDF files and extracts what automatic top-up crediting needs: the unique
// receipt number, the amount, the payment time, the seller, a masked payer and
// signs that the file was edited.
//
// Calibration (ARCHITECTURE §6.1): Kaspi generates its receipts with
// WeasyPrint from a web page as a single-revision PDF 1.7 (object and cross
// reference streams with exact offsets, WeasyPrint's header comment), text
// drawn through Roboto subsets with ToUnicode maps, no Creator, no ModDate
// and no XMP metadata. The receipt carries no QR code;
// "QR…" is the prefix of the receipt number ("№ чека"). Images on the page
// (the seller's avatar) are still scanned for QR codes in case Kaspi adds one,
// and a QR payload that names a different receipt number blocks crediting.
//
// Parse never panics and bounds its work; any document it cannot read with
// confidence is reported (parse status and flags) so a person reviews it.
package receipt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/pdfdoc"
)

// Provider is payment_receipts.provider and payment_transactions.provider of
// receipts credited from a Kaspi PDF.
const Provider = "kaspi_pdf"

// Parse statuses (payment_receipts.parse_status).
const (
	StatusParsed     = "parsed"     // every required field was read
	StatusPartial    = "partial"    // readable, but some required field is missing
	StatusUnreadable = "unreadable" // not a PDF this package can read
)

// Required fields reported in Receipt.Missing.
const (
	FieldSerial    = "serial"
	FieldAmount    = "amount"
	FieldPaidAt    = "paid_at"
	FieldRecipient = "recipient"
	FieldStatus    = "status"
)

// Flags. Every flag blocks automatic crediting except the informational
// ones listed in infoFlags.
const (
	FlagUnreadable         = "pdf_unreadable"
	FlagIncrementalUpdate  = "incremental_update"   // more than one revision (%%EOF, startxref, /Prev)
	FlagObjectRedefined    = "object_redefined"     // an object number defined twice
	FlagTrailingData       = "trailing_data"        // bytes after the last %%EOF
	FlagStructure          = "structure_unexpected" // not exactly one %%EOF, startxref and cross-reference section
	FlagXRefInconsistent   = "xref_inconsistent"    // offsets that do not match the file: bytes changed afterwards
	FlagHeaderUnexpected   = "header_unexpected"    // the header comment differs from WeasyPrint's
	FlagProducerUnexpected = "producer_unexpected"  // Producer is not WeasyPrint
	FlagCreatorPresent     = "creator_present"      // Kaspi receipts have no Creator
	FlagModDatePresent     = "moddate_present"      // Kaspi receipts have no ModDate
	FlagXMPMetadata        = "xmp_metadata_present" // editors add XMP; Kaspi does not
	FlagPageCount          = "page_count_unexpected"
	FlagFontUnexpected     = "font_unexpected" // text in a font other than Roboto
	FlagTextUndecodable    = "text_undecodable"
	FlagAnnotations        = "annotations_present" // overlays drawn outside the page content
	FlagForm               = "form_present"
	FlagQRMismatch         = "qr_mismatch" // a QR payload names another receipt number
	FlagSerialAmbiguous    = "serial_ambiguous"
	FlagSerialLabelMissing = "serial_label_missing"
	FlagSerialFormat       = "serial_format_unexpected"
	FlagAmountAmbiguous    = "amount_ambiguous"
	FlagAmountFractional   = "amount_fractional"
	FlagStatusMissing      = "status_missing"
	FlagFileNameMismatch   = "filename_serial_mismatch" // receipt_QR….pdf names another number

	FlagQRNotFound   = "qr_not_found"  // informational: Kaspi receipts carry no QR code
	FlagBINMissing   = "bin_missing"   // informational: no seller BIN printed (payments then requires review)
	FlagStatusKazakh = "status_kazakh" // informational: Kazakh status wording (uncalibrated)
)

var infoFlags = map[string]bool{FlagQRNotFound: true, FlagBINMissing: true, FlagStatusKazakh: true}

// MaxBytes bounds the receipt files Parse accepts; Kaspi receipts are ~25 KB.
const MaxBytes = 5 << 20

// limits bound the PDF reader on one receipt.
var limits = pdfdoc.Limits{
	MaxObjects: 5_000, MaxStreamBytes: 4 << 20, MaxTotalBytes: 16 << 20,
	MaxPages: 10, MaxOps: 100_000, MaxImagePixels: 2_000_000,
}

// weasyHeader is the first two lines WeasyPrint (pydyf) writes: the version
// and a binary comment with U+1F5A4. Other writers (qpdf, pikepdf, pypdf,
// PDF editors, printing to PDF) write their own comment.
var weasyHeader = regexp.MustCompile(`^%PDF-[12]\.[0-9]\r?\n%\x{1F5A4}\r?\n`)

// maxImages bounds the images scanned for QR codes.
const maxImages = 6

// Receipt is what Parse read from a file.
type Receipt struct {
	FileSHA256 string // hex SHA-256 of the exact bytes
	Serial     string // receipt number, upper case without spaces, e.g. "QR18149336524"
	Amount     int64  // KZT; 0 when missing
	PaidAt     *time.Time
	// RecipientName and RecipientBIN identify the seller as printed.
	RecipientName string
	RecipientBIN  string
	PayerMasked   string // e.g. "Е*** Б."; the full name is never kept outside the file
	PaymentMethod string // e.g. "с Kaspi Gold"
	Producer      string
	// QRPayloadHash is the hex SHA-256 of the first decoded QR payload.
	QRPayloadHash string
	ParseStatus   string
	Flags         []string // sorted
	Missing       []string // required fields not found
}

// Blocking returns the flags that forbid automatic crediting.
func (r Receipt) Blocking() []string {
	var out []string
	for _, f := range r.Flags {
		if !infoFlags[f] {
			out = append(out, f)
		}
	}
	return out
}

// HasFlag reports whether f was raised.
func (r Receipt) HasFlag(f string) bool { return slices.Contains(r.Flags, f) }

// Options tune Parse.
type Options struct {
	// FileName is the client's file name ("receipt_QR18149336524.pdf" when
	// shared from Kaspi); a receipt number in it must match the text.
	FileName string
}

// Astana is the time zone of "Дата и время по Астане": all of Kazakhstan has
// been on UTC+5 since 1 March 2024. A fixed zone needs no tzdata.
var Astana = time.FixedZone("UTC+05", 5*60*60)

// Parse reads a receipt. It always returns a Receipt; a file that is not a
// readable PDF has ParseStatus StatusUnreadable.
func Parse(ctx context.Context, data []byte, opt Options) (r Receipt) {
	sum := sha256.Sum256(data)
	r.FileSHA256 = hex.EncodeToString(sum[:])
	flags := map[string]bool{}
	defer func() {
		if rec := recover(); rec != nil {
			flags = map[string]bool{FlagUnreadable: true}
			r = Receipt{FileSHA256: r.FileSHA256, ParseStatus: StatusUnreadable}
		}
		r.Flags = sortedKeys(flags)
	}()
	if len(data) > MaxBytes {
		flags[FlagUnreadable] = true
		r.ParseStatus = StatusUnreadable
		return r
	}
	doc, err := pdfdoc.Open(data, limits)
	if err != nil {
		flags[FlagUnreadable] = true
		r.ParseStatus = StatusUnreadable
		return r
	}
	structureFlags(doc, data, flags, &r)
	pages := doc.Pages()
	if len(pages) != 1 {
		flags[FlagPageCount] = true
	}
	if len(pages) == 0 {
		flags[FlagUnreadable] = true
		r.ParseStatus = StatusUnreadable
		return r
	}
	page := pages[0]
	if doc.Annotations(page) > 0 {
		flags[FlagAnnotations] = true
	}
	text, err := doc.ExtractText(page)
	if err != nil || text.Undecodable > 0 {
		flags[FlagTextUndecodable] = true
	}
	for _, f := range text.Fonts {
		if !strings.HasPrefix(f, "Roboto") {
			flags[FlagFontUnexpected] = true
		}
	}
	items := make([]string, 0, len(text.Runs))
	for _, run := range text.Runs {
		if s := cleanItem(run.Text); s != "" {
			items = append(items, s)
		}
	}
	extractFields(items, &r, flags)
	if opt.FileName != "" && r.Serial != "" {
		for _, s := range findSerials(opt.FileName) {
			if s != r.Serial {
				flags[FlagFileNameMismatch] = true
			}
		}
	}
	if ctx.Err() == nil {
		scanQR(ctx, doc, page, &r, flags)
	}
	r.Missing = missing(r, flags)
	if len(r.Missing) == 0 {
		r.ParseStatus = StatusParsed
	} else {
		r.ParseStatus = StatusPartial
	}
	return r
}

// structureFlags checks the revision structure and the metadata against
// the calibrated Kaspi originals.
func structureFlags(doc *pdfdoc.Doc, data []byte, flags map[string]bool, r *Receipt) {
	if doc.EOFMarkers > 1 || doc.StartXRefs > 1 || doc.HasPrev || doc.Trailers() > 1 || doc.XRefTables+doc.XRefStreams > 1 {
		flags[FlagIncrementalUpdate] = true
	}
	if doc.EOFMarkers == 0 || doc.StartXRefs == 0 || doc.XRefTables+doc.XRefStreams == 0 {
		flags[FlagStructure] = true
	}
	if doc.XRefMismatches() > 0 {
		flags[FlagXRefInconsistent] = true
	}
	if !weasyHeader.Match(data[:min(len(data), 32)]) {
		flags[FlagHeaderUnexpected] = true
	}
	if doc.Redefined > 0 {
		flags[FlagObjectRedefined] = true
	}
	if doc.TrailingBytes > 0 {
		flags[FlagTrailingData] = true
	}
	producer, _ := doc.InfoString("Producer")
	r.Producer = clip(strings.TrimSpace(producer), 200)
	if !strings.HasPrefix(r.Producer, "WeasyPrint") {
		flags[FlagProducerUnexpected] = true
	}
	if c, ok := doc.InfoString("Creator"); ok && strings.TrimSpace(c) != "" {
		flags[FlagCreatorPresent] = true
	}
	if _, ok := doc.InfoString("ModDate"); ok {
		flags[FlagModDatePresent] = true
	}
	if doc.HasXMPMetadata() {
		flags[FlagXMPMetadata] = true
	}
	if doc.HasForm() {
		flags[FlagForm] = true
	}
}

func missing(r Receipt, flags map[string]bool) []string {
	var out []string
	if r.Serial == "" {
		out = append(out, FieldSerial)
	}
	if r.Amount <= 0 {
		out = append(out, FieldAmount)
	}
	if r.PaidAt == nil {
		out = append(out, FieldPaidAt)
	}
	if r.RecipientBIN == "" && r.RecipientName == "" {
		out = append(out, FieldRecipient)
	}
	if flags[FlagStatusMissing] {
		out = append(out, FieldStatus)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
