package pdfdoc

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// TextRun is text shown by one positioning of the text cursor: the strings of
// consecutive Tj/TJ/'/" operators without a Tm, Td, TD, T* or BT between them.
// X and Y are the text-space origin of the run (before the CTM).
type TextRun struct {
	Text     string
	X, Y     float64
	Font     string // BaseFont of the font used, subset prefix removed
	FontSize float64
}

// PageText is the text of a page in content-stream order.
type PageText struct {
	Runs []TextRun
	// Undecodable counts character codes that had no Unicode mapping.
	Undecodable int
	// Fonts lists the BaseFont names (subset prefix removed) used by text
	// operators, in first-use order.
	Fonts []string
}

// ExtractText interprets the page's content streams (and the Form XObjects
// they draw, up to a small depth) and returns the shown text.
func (d *Doc) ExtractText(p Page) (PageText, error) {
	content, err := d.pageContent(p)
	if err != nil {
		return PageText{}, err
	}
	in := &interp{d: d, fonts: map[Ref]*font{}, seen: map[string]bool{}}
	err = in.run(content, p.Resources, 0, map[Ref]bool{})
	in.flush()
	return in.out, err
}

// pageContent concatenates the decoded content streams of a page.
func (d *Doc) pageContent(p Page) ([]byte, error) {
	var parts []*Stream
	switch c := d.Resolve(p.Dict["Contents"]).(type) {
	case *Stream:
		parts = append(parts, c)
	case Array:
		for _, o := range c {
			if s, ok := d.Resolve(o).(*Stream); ok {
				parts = append(parts, s)
			}
			if len(parts) > 256 {
				return nil, ErrTooLarge
			}
		}
	}
	var buf bytes.Buffer
	for _, s := range parts {
		data, err := d.Decode(s)
		if err != nil {
			return nil, err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// matrix is an affine transform [a b c d e f].
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

type interp struct {
	d     *Doc
	fonts map[Ref]*font // by font dictionary reference
	out   PageText
	seen  map[string]bool

	cur      *strings.Builder
	curRun   TextRun
	font     *font
	fontSize float64
	tm, tlm  matrix
	leading  float64
}

func (in *interp) flush() {
	if in.cur == nil {
		return
	}
	in.curRun.Text = in.cur.String()
	if in.curRun.Text != "" {
		in.out.Runs = append(in.out.Runs, in.curRun)
	}
	in.cur = nil
}

func (in *interp) moveTo(m matrix) {
	in.flush()
	in.tm, in.tlm = m, m
}

func (in *interp) show(s String) {
	if in.cur == nil {
		in.cur = &strings.Builder{}
		in.curRun = TextRun{X: in.tm[4], Y: in.tm[5], FontSize: in.fontSize}
		if in.font != nil {
			in.curRun.Font = in.font.baseName
		}
	}
	if in.font == nil {
		in.out.Undecodable += len(s)
		return
	}
	if in.cur.Len() > 64<<10 {
		return
	}
	text, bad := in.font.decode(s)
	in.out.Undecodable += bad
	in.cur.WriteString(text)
}

// run interprets one content stream with the given resources.
func (in *interp) run(content []byte, res Dict, depth int, visited map[Ref]bool) error {
	l := &lexer{b: content}
	var stack []Object
	for {
		tok, err := l.next(0, false)
		if err == io.EOF {
			return nil
		}
		if err == errTooDeep || err == ErrTooLarge {
			return err
		}
		kw, isOp := tok.(keyword)
		if !isOp {
			if len(stack) >= 64 {
				stack = stack[1:]
			}
			stack = append(stack, tok)
			continue
		}
		in.d.ops++
		if in.d.ops > in.d.lim.MaxOps {
			return ErrTooLarge
		}
		in.op(string(kw), stack, res, depth, visited, l)
		stack = stack[:0]
	}
}

func nums(args []Object, n int) ([]float64, bool) {
	if len(args) < n {
		return nil, false
	}
	out := make([]float64, n)
	for i, a := range args[len(args)-n:] {
		v, ok := Number(a)
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

func (in *interp) op(op string, args []Object, res Dict, depth int, visited map[Ref]bool, l *lexer) {
	switch op {
	case "BT":
		in.moveTo(identity)
	case "ET":
		in.flush()
	case "Tf":
		if len(args) >= 2 {
			if name, ok := args[len(args)-2].(Name); ok {
				in.font = in.loadFont(name, res)
			}
			if v, ok := Number(args[len(args)-1]); ok {
				in.fontSize = v
			}
			if in.font != nil && !in.seen[in.font.baseName] {
				in.seen[in.font.baseName] = true
				in.out.Fonts = append(in.out.Fonts, in.font.baseName)
			}
		}
	case "Tm":
		if v, ok := nums(args, 6); ok {
			in.moveTo(matrix{v[0], v[1], v[2], v[3], v[4], v[5]})
		}
	case "Td", "TD":
		if v, ok := nums(args, 2); ok {
			if op == "TD" {
				in.leading = -v[1]
			}
			in.moveTo(matrix{1, 0, 0, 1, v[0], v[1]}.mul(in.tlm))
		}
	case "TL":
		if v, ok := nums(args, 1); ok {
			in.leading = v[0]
		}
	case "T*":
		in.moveTo(matrix{1, 0, 0, 1, 0, -in.leading}.mul(in.tlm))
	case "Tj":
		if len(args) > 0 {
			if s, ok := args[len(args)-1].(String); ok {
				in.show(s)
			}
		}
	case "'", "\"":
		in.moveTo(matrix{1, 0, 0, 1, 0, -in.leading}.mul(in.tlm))
		if len(args) > 0 {
			if s, ok := args[len(args)-1].(String); ok {
				in.show(s)
			}
		}
	case "TJ":
		if len(args) == 0 {
			return
		}
		arr, ok := args[len(args)-1].(Array)
		if !ok {
			return
		}
		for _, item := range arr {
			switch v := item.(type) {
			case String:
				in.show(v)
			default:
				// A large negative adjustment is a word gap in generators
				// that do not emit space glyphs.
				if n, ok := Number(v); ok && n <= -250 && in.cur != nil {
					if s := in.cur.String(); s != "" && !strings.HasSuffix(s, " ") {
						in.cur.WriteByte(' ')
					}
				}
			}
		}
	case "BI":
		skipInlineImage(l)
	case "Do":
		if depth >= 4 || len(args) == 0 {
			return
		}
		name, ok := args[len(args)-1].(Name)
		if !ok {
			return
		}
		ref := in.d.dict(res["XObject"])[name]
		r, isRef := ref.(Ref)
		if isRef {
			if visited[r] {
				return
			}
			visited[r] = true
			defer delete(visited, r)
		}
		s, ok := in.d.Resolve(ref).(*Stream)
		if !ok || s.Dict.Name("Subtype") != "Form" {
			return
		}
		data, err := in.d.Decode(s)
		if err != nil {
			return
		}
		formRes := res
		if fr := in.d.dict(s.Dict["Resources"]); fr != nil {
			formRes = fr
		}
		saveTM, saveTLM, saveFont, saveSize := in.tm, in.tlm, in.font, in.fontSize
		in.flush()
		_ = in.run(data, formRes, depth+1, visited)
		in.flush()
		in.tm, in.tlm, in.font, in.fontSize = saveTM, saveTLM, saveFont, saveSize
	}
}

// skipInlineImage moves past "BI … ID <data> EI".
func skipInlineImage(l *lexer) {
	i := bytes.Index(l.b[l.pos:], []byte("ID"))
	if i < 0 {
		l.pos = len(l.b)
		return
	}
	p := l.pos + i + 2
	for p < len(l.b) {
		j := bytes.Index(l.b[p:], []byte("EI"))
		if j < 0 {
			l.pos = len(l.b)
			return
		}
		e := p + j
		before := e > 0 && isWhite(l.b[e-1])
		after := e+2 == len(l.b) || isWhite(l.b[e+2]) || isDelim(l.b[e+2])
		if before && after {
			l.pos = e + 2
			return
		}
		p = e + 2
	}
	l.pos = len(l.b)
}

// font decodes character codes of one font resource.
type font struct {
	baseName string
	ranges   []codeRange // code space; empty means fixed codeLen
	codeLen  int
	cmap     map[uint32]string
	simple   bool // one-byte codes without ToUnicode: Windows-1252
}

type codeRange struct{ lo, hi []byte }

func (in *interp) loadFont(name Name, res Dict) *font {
	obj := in.d.dict(res["Font"])[name]
	ref, isRef := obj.(Ref)
	if isRef {
		if f, ok := in.fonts[ref]; ok {
			return f
		}
	}
	fd := in.d.dict(obj)
	if fd == nil {
		return nil
	}
	f := &font{baseName: stripSubset(string(fd.Name("BaseFont"))), codeLen: 1}
	if fd.Name("Subtype") == "Type0" {
		f.codeLen = 2
	}
	if s, ok := in.d.Resolve(fd["ToUnicode"]).(*Stream); ok {
		if data, err := in.d.Decode(s); err == nil {
			f.cmap, f.ranges = parseCMap(data)
		}
	}
	if f.cmap == nil && f.codeLen == 1 {
		f.simple = true
	}
	if isRef && len(in.fonts) < 256 {
		in.fonts[ref] = f
	}
	return f
}

// stripSubset removes the "ABCDEF+" subset tag of an embedded font name.
func stripSubset(s string) string {
	if len(s) > 7 && s[6] == '+' {
		for i := 0; i < 6; i++ {
			if s[i] < 'A' || s[i] > 'Z' {
				return s
			}
		}
		return s[7:]
	}
	return s
}

var win1252 = charmap.Windows1252

// decode maps a shown string to text; bad counts unmapped codes.
func (f *font) decode(s String) (string, int) {
	var b strings.Builder
	bad := 0
	if f.simple {
		for _, c := range s {
			b.WriteRune(win1252.DecodeByte(c))
		}
		return b.String(), 0
	}
	for i := 0; i < len(s); {
		n := f.codeLength(s[i:])
		if i+n > len(s) {
			bad++
			break
		}
		var code uint32
		for _, c := range s[i : i+n] {
			code = code<<8 | uint32(c)
		}
		i += n
		if t, ok := f.cmap[code]; ok {
			b.WriteString(t)
			continue
		}
		bad++
		b.WriteRune('�')
	}
	return b.String(), bad
}

func (f *font) codeLength(s []byte) int {
	for n := 1; n <= 4 && n <= len(s); n++ {
		for _, r := range f.ranges {
			if len(r.lo) != n {
				continue
			}
			in := true
			for k := 0; k < n; k++ {
				if s[k] < r.lo[k] || s[k] > r.hi[k] {
					in = false
					break
				}
			}
			if in {
				return n
			}
		}
	}
	return f.codeLen
}

// CMap limits.
const (
	maxCMapEntries = 70_000
	maxRangeSpan   = 65_536
)

// parseCMap reads the bfchar/bfrange mappings and the code space ranges of a
// ToUnicode CMap.
func parseCMap(data []byte) (map[uint32]string, []codeRange) {
	m := map[uint32]string{}
	var ranges []codeRange
	l := &lexer{b: data}
	var args []Object
	for {
		tok, err := l.next(0, false)
		if err != nil {
			break
		}
		kw, ok := tok.(keyword)
		if !ok {
			if len(args) < 3*1000 {
				args = append(args, tok)
			}
			continue
		}
		switch kw {
		case "endcodespacerange":
			for i := 0; i+1 < len(args); i += 2 {
				lo, ok1 := args[i].(String)
				hi, ok2 := args[i+1].(String)
				if ok1 && ok2 && len(lo) == len(hi) && len(lo) >= 1 && len(lo) <= 4 && len(ranges) < 64 {
					ranges = append(ranges, codeRange{lo: lo, hi: hi})
				}
			}
		case "endbfchar":
			for i := 0; i+1 < len(args) && len(m) < maxCMapEntries; i += 2 {
				src, ok := args[i].(String)
				if !ok || len(src) == 0 || len(src) > 4 {
					continue
				}
				if dst, ok := args[i+1].(String); ok {
					m[codeOf(src)] = utf16String(dst)
				}
			}
		case "endbfrange":
			for i := 0; i+2 < len(args) && len(m) < maxCMapEntries; i += 3 {
				lo, ok1 := args[i].(String)
				hi, ok2 := args[i+1].(String)
				if !ok1 || !ok2 || len(lo) == 0 || len(lo) > 4 || len(lo) != len(hi) {
					continue
				}
				a, b := codeOf(lo), codeOf(hi)
				if b < a || b-a >= maxRangeSpan {
					continue
				}
				switch dst := args[i+2].(type) {
				case String:
					units := utf16Units(dst)
					if len(units) == 0 {
						continue
					}
					for c := a; c <= b && len(m) < maxCMapEntries; c++ {
						u := append([]uint16(nil), units...)
						u[len(u)-1] += uint16(c - a)
						m[c] = string(utf16.Decode(u))
					}
				case Array:
					for k, o := range dst {
						if a+uint32(k) > b || len(m) >= maxCMapEntries {
							break
						}
						if s, ok := o.(String); ok {
							m[a+uint32(k)] = utf16String(s)
						}
					}
				}
			}
		}
		args = args[:0]
	}
	return m, ranges
}

func codeOf(s []byte) uint32 {
	var c uint32
	for _, b := range s {
		c = c<<8 | uint32(b)
	}
	return c
}

func utf16Units(s []byte) []uint16 {
	u := make([]uint16, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		u = append(u, uint16(s[i])<<8|uint16(s[i+1]))
	}
	return u
}

func utf16String(s []byte) string {
	if len(s) == 1 {
		return string(rune(s[0]))
	}
	return string(utf16.Decode(utf16Units(s)))
}
