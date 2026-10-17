package pdfdoc

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

func TestLexerObjects(t *testing.T) {
	l := &lexer{b: []byte(`<< /Type /Pa#67e /Kids [1 0 R 2 0 R] /N -3.5 /S (a\(b\)\101\
c) /H <48 65 6C6c 6F> /B true /Z null /Nested << /A [ [ 1 2 ] ] >> >>`)}
	o, err := l.next(0, true)
	if err != nil {
		t.Fatal(err)
	}
	d := o.(Dict)
	if d["Type"] != Name("Page") {
		t.Errorf("name escape: %v", d["Type"])
	}
	kids := d["Kids"].(Array)
	if len(kids) != 2 || kids[0] != (Ref{1, 0}) || kids[1] != (Ref{2, 0}) {
		t.Errorf("kids: %v", kids)
	}
	if d["N"] != -3.5 || string(d["S"].(String)) != "a(b)Ac" || string(d["H"].(String)) != "Hello" ||
		d["B"] != true || d["Z"] != nil {
		t.Errorf("values: %#v", d)
	}
	if _, ok := d["Nested"].(Dict)["A"].(Array)[0].(Array); !ok {
		t.Errorf("nested: %#v", d["Nested"])
	}
}

func TestLexerNestingLimit(t *testing.T) {
	l := &lexer{b: bytes.Repeat([]byte("["), 10_000)}
	if _, err := l.next(0, true); err != errTooDeep {
		t.Fatalf("err = %v", err)
	}
}

func deflate(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// classicPDF builds a small PDF with a classic xref table and a WinAnsi font.
func classicPDF(content string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	objs := []string{
		"<</Type /Catalog /Pages 2 0 R>>",
		"<</Type /Pages /Kids [3 0 R] /Count 1 /Resources <</Font <</F1 5 0 R>>>>>>",
		"<</Type /Page /Parent 2 0 R /Contents 4 0 R>>",
		"",
		"<</Type /Font /Subtype /Type1 /BaseFont /Helvetica>>",
	}
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		if i == 3 {
			data := deflate([]byte(content))
			fmt.Fprintf(&b, "4 0 obj\n<</Length %d /Filter /FlateDecode>>\nstream\n", len(data))
			b.Write(data)
			b.WriteString("\nendstream\nendobj\n")
			continue
		}
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<</Size %d /Root 1 0 R /Info <</Producer (Test)>>>>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func TestClassicDocument(t *testing.T) {
	pdf := classicPDF("BT /F1 12 Tf 10 20 Td (Caf\\351 total) Tj 0 -14 Td [(5)-300(000)] TJ T* (x) ' ET")
	d, err := Open(pdf, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "1.4" || d.EOFMarkers != 1 || d.StartXRefs != 1 || d.XRefTables != 1 || d.HasPrev || d.Redefined != 0 {
		t.Fatalf("structure: %+v", d)
	}
	if p, _ := d.InfoString("Producer"); p != "Test" {
		t.Fatalf("producer %q", p)
	}
	pages := d.Pages()
	if len(pages) != 1 || pages[0].Resources == nil {
		t.Fatalf("pages: %+v", pages)
	}
	txt, err := d.ExtractText(pages[0])
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range txt.Runs {
		got = append(got, r.Text)
	}
	if strings.Join(got, "|") != "Café total|5 000|x" || txt.Fonts[0] != "Helvetica" {
		t.Fatalf("runs %q fonts %v", got, txt.Fonts)
	}
}

func TestIncrementalUpdateIsVisible(t *testing.T) {
	pdf := classicPDF("BT /F1 12 Tf (one) Tj ET")
	var b bytes.Buffer
	b.Write(pdf)
	data := deflate([]byte("BT /F1 12 Tf (two) Tj ET"))
	fmt.Fprintf(&b, "4 0 obj\n<</Length %d /Filter /FlateDecode>>\nstream\n", len(data))
	b.Write(data)
	b.WriteString("\nendstream\nendobj\nxref\n0 1\n0000000000 65535 f \ntrailer\n<</Size 6 /Root 1 0 R /Prev 9>>\nstartxref\n1\n%%EOF\n")
	d, err := Open(b.Bytes(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasPrev || d.EOFMarkers != 2 || d.StartXRefs != 2 || d.Redefined != 1 || d.Trailers() != 2 {
		t.Fatalf("structure: %+v", d)
	}
	txt, _ := d.ExtractText(d.Pages()[0])
	if len(txt.Runs) != 1 || txt.Runs[0].Text != "two" {
		t.Fatalf("runs %+v", txt.Runs)
	}
}

func TestPageTreeCycle(t *testing.T) {
	pdf := []byte("%PDF-1.7\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj\n2 0 obj <</Type /Pages /Kids [2 0 R 3 0 R 2 0 R]>> endobj\n3 0 obj <</Type /Page /Parent 2 0 R>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF")
	d, err := Open(pdf, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(d.Pages()); n != 1 {
		t.Fatalf("pages = %d", n)
	}
}

func TestActiveContent(t *testing.T) {
	pdf := []byte("%PDF-1.7\n1 0 obj <</Type /Catalog /Pages 2 0 R /OpenAction 3 0 R>> endobj\n2 0 obj <</Type /Pages /Kids []>> endobj\n3 0 obj <</S /Java#53cript /J#53 (app.alert(1))>> endobj\ntrailer <</Root 1 0 R>>\n%%EOF")
	d, err := Open(pdf, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.ActiveContent(); strings.Join(got, ",") != "JS,JavaScript" {
		t.Fatalf("active = %v", got)
	}
}

func TestEncryptedAndBombs(t *testing.T) {
	enc := []byte("%PDF-1.7\n1 0 obj <</Type /Catalog>> endobj\ntrailer <</Root 1 0 R /Encrypt <</Filter /Standard>>>>\n%%EOF")
	if d, err := Open(enc, Limits{}); err != nil || !d.Encrypted {
		t.Fatalf("encrypted: %v %v", d, err)
	}
	bomb := deflate(bytes.Repeat([]byte{0}, 64<<20))
	pdf := fmt.Appendf(nil, "%%PDF-1.7\n1 0 obj <</Type /Catalog /Pages 2 0 R>> endobj\n2 0 obj <</Type /Pages /Kids [3 0 R]>> endobj\n3 0 obj <</Type /Page /Contents 4 0 R>> endobj\n4 0 obj <</Length %d /Filter /FlateDecode>> stream\n", len(bomb))
	pdf = append(pdf, bomb...)
	pdf = append(pdf, []byte("\nendstream endobj\ntrailer <</Root 1 0 R>>\n%%EOF")...)
	d, err := Open(pdf, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExtractText(d.Pages()[0]); err != ErrTooLarge {
		t.Fatalf("bomb: %v", err)
	}
}

func TestNotPDF(t *testing.T) {
	if _, err := Open([]byte("GIF89a"), Limits{}); err != ErrNotPDF {
		t.Fatalf("err = %v", err)
	}
}

// FuzzOpen runs every reader entry point on arbitrary input.
// Run: go test -run '^$' -fuzz FuzzOpen -fuzztime 60s ./admin/backend/internal/platform/pdfdoc
func FuzzOpen(f *testing.F) {
	f.Add(classicPDF("BT /F1 12 Tf (a) Tj ET"))
	f.Add([]byte("%PDF-1.7\n1 0 obj <</Type /ObjStm /N 2 /First 8 /Length 20>> stream\n2 0 3 5 <<>> [1 0 R]\nendstream endobj trailer <</Root 2 0 R>>"))
	f.Add([]byte("%PDF-1.7\n1 0 obj <</Type /XObject /Subtype /Image /Width 2 /Height 2 /BitsPerComponent 8 /ColorSpace [/Indexed /DeviceRGB 1 <000000FFFFFF>] /Length 4>> stream\n\x00\x01\x01\x00\nendstream endobj"))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Open(data, Limits{MaxObjects: 2000, MaxTotalBytes: 4 << 20, MaxOps: 20_000})
		if err != nil {
			return
		}
		_ = d.ActiveContent()
		_, _ = d.InfoString("Producer")
		for _, p := range d.Pages() {
			_, _ = d.ExtractText(p)
			_, _ = d.PageImages(p, 4)
			_ = d.Annotations(p)
		}
	})
}
