// Package receipttest builds synthetic Kaspi receipt PDFs for tests. The
// files mimic the structure of real Kaspi receipts (ARCHITECTURE §6.1):
// PDF 1.7 written once, catalog and fonts in an object stream, a cross
// reference stream, Type0 Identity-H fonts with ToUnicode maps, text drawn
// with TJ, Producer "WeasyPrint 70.0", no Creator, an avatar image with a
// soft mask. All names and numbers are invented.
package receipttest

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// Spec describes a receipt. Zero values take the defaults of Default().
type Spec struct {
	Status  string // first line, "Оплата совершена"
	Amount  string // printed amount before "₸", e.g. "5 000"
	Seller  string // ИП "JAN-TAXI"
	Serial  string // "QR18149336524"
	BIN     string // seller IIN/BIN
	Payer   string // "Тест Т." (invented)
	PaidAt  time.Time
	Method  string // "с Kaspi Gold"
	Address string
	// Producer of the Info dictionary; "-" omits it.
	Producer string
	Creator  string // empty: none (like Kaspi)
	ModDate  bool
	// QRPayload, when set, adds a QR code image with this text.
	QRPayload string
	// Font is the BaseFont of the regular text font (default "Roboto").
	Font string
	// NoSerialLabel drops the "№ чека" label line (the number stays).
	NoSerialLabel bool
	// NoBINLine drops the seller IIN/BIN line.
	NoBINLine bool
	// Annotation adds a FreeText annotation to the page.
	Annotation bool
	// Header replaces the binary comment line after "%PDF-1.7" (WeasyPrint
	// writes "%" + U+1F5A4; qpdf, for one, writes "%\xbf\xf7\xa2\xfe").
	Header string
}

// Astana is UTC+5.
var Astana = time.FixedZone("UTC+05", 5*60*60)

// Default is a valid receipt for 500 ₸ paid now to ИП "JAN-TAXI".
func Default() Spec {
	return Spec{
		Status: "Оплата совершена", Amount: "500 ", Seller: `ИП "JAN-TAXI"`, Serial: "QR18149336524",
		BIN: "000809500289", Payer: "Тест Т.", PaidAt: time.Now(), Method: "с Kaspi Gold",
		Address: "г. С.жалагаш, Н.Мырзалиев, 94", Producer: "WeasyPrint 70.0", Font: "Roboto",
	}
}

func (s Spec) withDefaults() Spec {
	d := Default()
	if s.Status == "" {
		s.Status = d.Status
	}
	if s.Amount == "" {
		s.Amount = d.Amount
	}
	if s.Seller == "" {
		s.Seller = d.Seller
	}
	if s.Serial == "" {
		s.Serial = d.Serial
	}
	if s.BIN == "" {
		s.BIN = d.BIN
	}
	if s.Payer == "" {
		s.Payer = d.Payer
	}
	if s.PaidAt.IsZero() {
		s.PaidAt = d.PaidAt
	}
	if s.Method == "" {
		s.Method = d.Method
	}
	if s.Address == "" {
		s.Address = d.Address
	}
	if s.Producer == "" {
		s.Producer = d.Producer
	}
	if s.Font == "" {
		s.Font = d.Font
	}
	return s
}

type item struct {
	text string
	bold bool
	x, y float64
	size float64
}

func (s Spec) items() []item {
	label := func(t string, y float64) item { return item{text: t, x: 58.8, y: y, size: 14} }
	value := func(t string, y float64) item { return item{text: t, x: 300, y: y, size: 14} }
	out := []item{
		{text: s.Status, bold: true, x: 58.8, y: 173.6, size: 17},
		{text: s.Amount, bold: true, x: 58.8, y: 210.9, size: 34},
		{text: "₸", bold: true, x: 125.8, y: 210.9, size: 34},
		{text: s.Seller, x: 109.8, y: 282.6, size: 17},
	}
	if !s.NoSerialLabel {
		out = append(out, label("№ чека", 339.6))
	}
	out = append(out, value(s.Serial, 339.6))
	if !s.NoBINLine {
		out = append(out, label("ИИН/БИН продавца", 378.6), value(s.BIN, 378.6))
	}
	out = append(out,
		label("ФИО покупателя", 417.6), value(s.Payer, 417.6),
		label("Дата и время ", 456.6), label("по Астане", 474.6),
		value(s.PaidAt.In(Astana).Format("02.01.2006 15:04"), 456.6),
		label("Оплачено", 513.6), value(s.Method, 513.6),
		label("Адрес", 552.6), value(s.Address, 552.6),
		item{text: "Чек не является фискальным", x: 153.7, y: 588.9, size: 12},
	)
	return out
}

// glyphs assigns a two-byte code to every rune used by the specs.
type glyphs map[rune]uint16

func newGlyphs(specs ...Spec) glyphs {
	set := map[rune]bool{}
	for _, s := range specs {
		for _, it := range s.items() {
			for _, r := range it.text {
				set[r] = true
			}
		}
	}
	runes := make([]rune, 0, len(set))
	for r := range set {
		runes = append(runes, r)
	}
	sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })
	g := glyphs{}
	for i, r := range runes {
		g[r] = uint16(0x0100 + i)
	}
	return g
}

func (g glyphs) hex(text string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, r := range text {
		if i > 0 {
			b.WriteString("0")
		}
		fmt.Fprintf(&b, "<%04x>", g[r])
	}
	b.WriteString("] TJ")
	return b.String()
}

func (g glyphs) cmap() []byte {
	var b bytes.Buffer
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CIDSystemInfo\n<< /Registry (Adobe)\n/Ordering (UCS)\n/Supplement 0\n>> def\n/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n1 begincodespacerange\n<0000> <ffff>\nendcodespacerange\n")
	runes := make([]rune, 0, len(g))
	for r := range g {
		runes = append(runes, r)
	}
	sort.Slice(runes, func(i, j int) bool { return g[runes[i]] < g[runes[j]] })
	for start := 0; start < len(runes); start += 100 {
		end := min(start+100, len(runes))
		fmt.Fprintf(&b, "%d beginbfchar\n", end-start)
		for _, r := range runes[start:end] {
			u := utf16.Encode([]rune{r})
			fmt.Fprintf(&b, "<%04x> <", g[r])
			for _, x := range u {
				fmt.Fprintf(&b, "%04x", x)
			}
			b.WriteString(">\n")
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend")
	return b.Bytes()
}

func (s Spec) content(g glyphs, withQR bool) []byte {
	var b bytes.Buffer
	b.WriteString("1 0 0 -1 0 1440 cm\nq\n0.75 0 0 0.75 0 0 cm\nq\n1 1 1 rg\n37.795276 37.795276 404.409449 1844.409449 re\nf\nQ\n")
	for _, it := range s.items() {
		font := "/BNVCGR"
		if it.bold {
			font = "/KBXLHV"
		}
		fmt.Fprintf(&b, "0.121569 0.121569 0.121569 rg\nBT\n1 0 0 -1 %.6f %.6f Tm\n%s %g Tf\n%s\nET\n", it.x, it.y, font, it.size, g.hex(it.text))
	}
	b.WriteString("q\n1 0 0 1 59.795276 257.795276 cm\nq\n38 0 0 -38 0 38 cm\n/Avatar Do\nQ\nQ\n")
	if withQR {
		b.WriteString("q\n1 0 0 1 150 620 cm\nq\n120 0 0 -120 0 120 cm\n/Code Do\nQ\nQ\n")
	}
	b.WriteString("Q\n")
	return b.Bytes()
}

func deflate(data []byte) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, _ = w.Write(data)
	_ = w.Close()
	return b.Bytes()
}

// writer assembles a PDF and remembers object offsets.
type writer struct {
	buf     bytes.Buffer
	offsets map[int]int
}

func (w *writer) obj(num int, body string) {
	w.offsets[num] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n%s\nendobj\n", num, body)
}

func (w *writer) stream(num int, dict string, data []byte) {
	w.offsets[num] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n<<%s/Length %d>>\nstream\n", num, dict, len(data))
	w.buf.Write(data)
	w.buf.WriteString("\nendstream\nendobj\n")
}

func utf16Hex(s string) string {
	var b strings.Builder
	b.WriteString("<feff")
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04x", u)
	}
	b.WriteString(">")
	return b.String()
}

// Build returns a receipt PDF for s.
func Build(s Spec) []byte {
	pdf, _ := build(s.withDefaults())
	return pdf
}

// BuildSpliced returns edited written over original's cross-reference
// data: what changing the page content in place leaves behind when the
// offsets are not recomputed. The file is a single revision with the
// original header and metadata; only the offsets give the edit away.
func BuildSpliced(original, edited Spec) []byte {
	original, edited = original.withDefaults(), edited.withDefaults()
	g := newGlyphs(original, edited)
	_, stale := buildWith(original, g, nil)
	pdf, _ := buildWith(edited, g, &stale)
	return pdf
}

// BuildEdited returns original followed by an incremental update whose page
// content shows edited instead: what a PDF editor saves after "fixing" a
// receipt.
func BuildEdited(original, edited Spec) []byte {
	original, edited = original.withDefaults(), edited.withDefaults()
	g := newGlyphs(original, edited)
	pdf, lay := buildWith(original, g, nil)
	xref := lay.xrefOff
	var b bytes.Buffer
	b.Write(pdf)
	off := b.Len()
	data := deflate(edited.content(g, edited.QRPayload != ""))
	fmt.Fprintf(&b, "5 0 obj\n<</Filter /FlateDecode/Length %d>>\nstream\n", len(data))
	b.Write(data)
	b.WriteString("\nendstream\nendobj\n")
	xrefOff := b.Len()
	fmt.Fprintf(&b, "xref\n0 1\n0000000000 65535 f \n5 1\n%010d 00000 n \ntrailer\n<</Size 23/Root 2 0 R/Info 20 0 R/Prev %d>>\nstartxref\n%d\n%%%%EOF\n", off, xref, xrefOff)
	return b.Bytes()
}

// layout is where the objects and the cross-reference stream of a file are.
type layout struct {
	offsets map[int]int
	xrefOff int
}

func build(s Spec) ([]byte, layout) { return buildWith(s, newGlyphs(s), nil) }

// buildWith writes the receipt; with stale, the cross-reference data and
// startxref describe that layout instead of the bytes written.
func buildWith(s Spec, g glyphs, stale *layout) ([]byte, layout) {
	w := &writer{offsets: map[int]int{}}
	comment := "%\xf0\x9f\x96\xa4"
	if s.Header != "" {
		comment = s.Header
	}
	w.buf.WriteString("%PDF-1.7\n" + comment + "\n")
	withQR := s.QRPayload != ""

	w.stream(5, "/Filter /FlateDecode", deflate(s.content(g, withQR)))
	cm := deflate(g.cmap())
	w.stream(9, "/Filter /FlateDecode", cm)
	w.stream(13, "/Filter /FlateDecode", cm)
	avatar, alpha := avatarImage()
	w.stream(18, "/Type /XObject/Subtype /Image/Width 64/Height 64/ColorSpace /DeviceRGB/BitsPerComponent 8/Filter /FlateDecode/SMask 19 0 R", deflate(avatar))
	w.stream(19, "/Type /XObject/Subtype /Image/Width 64/Height 64/ColorSpace /DeviceGray/BitsPerComponent 8/Filter /FlateDecode", deflate(alpha))
	if withQR {
		gray, size := qrImage(s.QRPayload)
		w.stream(7, fmt.Sprintf("/Type /XObject/Subtype /Image/Width %d/Height %d/ColorSpace /DeviceGray/BitsPerComponent 8/Filter /FlateDecode", size, size), deflate(gray))
	}

	xobjects := "/Avatar 18 0 R"
	if withQR {
		xobjects += "/Code 7 0 R"
	}
	annots := ""
	if s.Annotation {
		annots = "/Annots [<</Type /Annot/Subtype /FreeText/Rect [50 1200 300 1240]/Contents (5000)/DA (/Helv 12 Tf 0 g)>>]"
	}
	compressed := []struct {
		num  int
		body string
	}{
		{1, "<</Type /Pages/Kids [6 0 R]/Count 1>>"},
		{2, "<</Type /Catalog/Pages 1 0 R/Lang (ru)>>"},
		{4, "<</ExtGState <<>>/XObject <<" + xobjects + ">>/Font 17 0 R>>"},
		{6, "<</Type /Page/Parent 1 0 R/MediaBox [0 0 360 1440]/Contents 5 0 R/Resources 4 0 R" + annots + ">>"},
		{10, "<</Type /FontDescriptor/FontName /KBXLHV+Roboto-Bold/FontFamily (Roboto)/Flags 4/FontBBox [0 -244 828 928]/ItalicAngle 0/Ascent 928/Descent -244/CapHeight 928/StemV 80/StemH 80>>"},
		{11, "<</Type /Font/Subtype /CIDFontType2/BaseFont /KBXLHV+Roboto-Bold/CIDSystemInfo <</Registry (Adobe)/Ordering (Identity)/Supplement 0>>/CIDToGIDMap /Identity/FontDescriptor 10 0 R>>"},
		{12, "<</Type /Font/Subtype /Type0/BaseFont /KBXLHV+Roboto-Bold/ToUnicode 9 0 R/Encoding /Identity-H/DescendantFonts [11 0 R]>>"},
		{14, "<</Type /FontDescriptor/FontName /BNVCGR+" + s.Font + "/FontFamily (" + s.Font + ")/Flags 4/FontBBox [0 -244 1028 928]/ItalicAngle 0/Ascent 928/Descent -244/CapHeight 928/StemV 80/StemH 80>>"},
		{15, "<</Type /Font/Subtype /CIDFontType2/BaseFont /BNVCGR+" + s.Font + "/CIDSystemInfo <</Registry (Adobe)/Ordering (Identity)/Supplement 0>>/CIDToGIDMap /Identity/FontDescriptor 14 0 R>>"},
		{16, "<</Type /Font/Subtype /Type0/BaseFont /BNVCGR+" + s.Font + "/ToUnicode 13 0 R/Encoding /Identity-H/DescendantFonts [15 0 R]>>"},
		{17, "<</KBXLHV 12 0 R/BNVCGR 16 0 R>>"},
		{20, s.info()},
	}
	var header, body bytes.Buffer
	for i, c := range compressed {
		if i > 0 {
			header.WriteByte(' ')
		}
		fmt.Fprintf(&header, "%d %d", c.num, body.Len())
		body.WriteString(c.body)
		body.WriteByte('\n')
	}
	header.WriteByte('\n')
	objstm := append(header.Bytes(), body.Bytes()...)
	w.stream(21, fmt.Sprintf("/Type /ObjStm/N %d/First %d/Filter /FlateDecode", len(compressed), header.Len()), deflate(objstm))

	// Cross-reference stream: W [1 4 2] for objects 0..22.
	index := map[int]int{}
	for i, c := range compressed {
		index[c.num] = i
	}
	xrefOff := w.buf.Len()
	w.offsets[22] = xrefOff
	written := layout{offsets: w.offsets, xrefOff: xrefOff}
	described := written
	if stale != nil {
		described = *stale
	}
	var xr bytes.Buffer
	for n := 0; n <= 22; n++ {
		var row [7]byte
		switch off, direct := described.offsets[n]; {
		case direct:
			row[0] = 1
			binary.BigEndian.PutUint32(row[1:5], uint32(off))
		case n != 0 && hasIndex(index, n):
			row[0] = 2
			binary.BigEndian.PutUint32(row[1:5], 21)
			binary.BigEndian.PutUint16(row[5:7], uint16(index[n]))
		default:
			row[5], row[6] = 0xFF, 0xFF
		}
		xr.Write(row[:])
	}
	data := deflate(xr.Bytes())
	fmt.Fprintf(&w.buf, "22 0 obj\n<</Type /XRef/Index [0 23]/W [1 4 2]/Size 23/Root 2 0 R/Info 20 0 R/Filter /FlateDecode/Length %d>>\nstream\n", len(data))
	w.buf.Write(data)
	fmt.Fprintf(&w.buf, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", described.xrefOff)
	return w.buf.Bytes(), written
}

func hasIndex(m map[int]int, n int) bool { _, ok := m[n]; return ok }

func (s Spec) info() string {
	var b strings.Builder
	b.WriteString("<<")
	if s.Producer != "-" {
		b.WriteString("/Producer (" + s.Producer + ")")
	}
	if s.Creator != "" {
		b.WriteString("/Creator (" + s.Creator + ")")
	}
	if s.ModDate {
		b.WriteString("/ModDate (D:20261017120000+05'00')")
	}
	b.WriteString("/Title " + utf16Hex("Чек") + ">>")
	return b.String()
}

// avatarImage is a 64×64 RGB gradient and its alpha mask (a disc).
func avatarImage() (rgb, alpha []byte) {
	rgb = make([]byte, 64*64*3)
	alpha = make([]byte, 64*64)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			i := y*64 + x
			rgb[i*3], rgb[i*3+1], rgb[i*3+2] = byte(x*4), byte(y*4), 0x80
			if dx, dy := x-32, y-32; dx*dx+dy*dy <= 32*32 {
				alpha[i] = 0xFF
			}
		}
	}
	return rgb, alpha
}

// qrImage renders payload as an 8-bit gray QR code.
func qrImage(payload string) ([]byte, int) {
	const size = 240
	m, err := qrcode.NewQRCodeWriter().Encode(payload, gozxing.BarcodeFormat_QR_CODE, size, size, nil)
	if err != nil {
		panic(err)
	}
	var img image.Image = m
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			out = append(out, byte(r>>8))
		}
	}
	return out, b.Dx()
}
