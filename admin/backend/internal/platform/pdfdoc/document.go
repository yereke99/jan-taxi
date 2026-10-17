package pdfdoc

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// Limits bound the work done on one document. Zero fields take the defaults.
type Limits struct {
	MaxObjects     int // objects, direct and in object streams (default 20 000)
	MaxStreamBytes int // one decoded stream (default 8 MiB)
	MaxTotalBytes  int // all decoded streams together (default 32 MiB)
	MaxPages       int // pages returned by Pages (default 50)
	MaxOps         int // content stream operators per document (default 300 000)
	MaxImagePixels int // pixels of one decoded image (default 4 000 000)
}

func (l Limits) withDefaults() Limits {
	if l.MaxObjects <= 0 {
		l.MaxObjects = 20_000
	}
	if l.MaxStreamBytes <= 0 {
		l.MaxStreamBytes = 8 << 20
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = 32 << 20
	}
	if l.MaxPages <= 0 {
		l.MaxPages = 50
	}
	if l.MaxOps <= 0 {
		l.MaxOps = 300_000
	}
	if l.MaxImagePixels <= 0 {
		l.MaxImagePixels = 4_000_000
	}
	return l
}

type entry struct {
	obj   Object
	gen   int
	order int // file offset of the definition (or of its object stream)
}

type trailer struct {
	dict  Dict
	order int
}

// Doc is a parsed document. It is not safe for concurrent use (stream
// decoding draws from a shared byte budget).
type Doc struct {
	data    []byte
	lim     Limits
	objects map[int]entry
	trls    []trailer
	trailer Dict
	budget  int
	ops     int

	// Version is the header version, e.g. "1.7".
	Version string
	// EOFMarkers counts "%%EOF" markers; StartXRefs counts "startxref"
	// keywords; XRefTables counts classic "xref" sections; XRefStreams
	// counts cross-reference streams. A file written once has one of each
	// kind it uses; incremental updates add more.
	EOFMarkers, StartXRefs, XRefTables, XRefStreams int
	// HasPrev is set when a trailer points to an earlier revision (/Prev).
	HasPrev bool
	// Redefined counts object numbers defined more than once.
	Redefined int
	// TrailingBytes counts non-white-space bytes after the last %%EOF.
	TrailingBytes int
	// Encrypted is set when a trailer has /Encrypt.
	Encrypted bool

	// startXRef is the offset after the last "startxref" (-1 when absent);
	// xrefTables are the positions of the classic "xref" keywords.
	startXRef  int64
	xrefTables []int
}

// Open parses data. It fails with ErrNotPDF when the header is missing and
// with ErrMalformed, ErrTooLarge or ErrUnsupported for documents it cannot
// read safely. It never panics.
func Open(data []byte, lim Limits) (doc *Doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("%w: %v", ErrMalformed, r)
		}
	}()
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, ErrNotPDF
	}
	lim = lim.withDefaults()
	d := &Doc{data: data, lim: lim, objects: map[int]entry{}, budget: lim.MaxTotalBytes, startXRef: -1}
	d.Version = headerVersion(data)
	d.EOFMarkers = bytes.Count(data, []byte("%%EOF"))
	if i := bytes.LastIndex(data, []byte("%%EOF")); i >= 0 {
		for _, c := range data[i+5:] {
			if !isWhite(c) {
				d.TrailingBytes++
			}
		}
	}
	if err := d.scan(); err != nil {
		return nil, err
	}
	if err := d.expandObjectStreams(); err != nil {
		return nil, err
	}
	d.mergeTrailers()
	if len(d.objects) == 0 {
		return nil, ErrMalformed
	}
	return d, nil
}

func headerVersion(data []byte) string {
	v := data[5:min(len(data), 13)]
	end := 0
	for end < len(v) && (isDigit(v[end]) || v[end] == '.') {
		end++
	}
	return string(v[:end])
}

// scan walks the file once, collecting "n g obj … endobj" definitions,
// classic xref sections, trailers and startxref keywords. Garbage between
// objects is skipped token by token.
func (d *Doc) scan() error {
	l := &lexer{b: d.data}
	for {
		l.skipSpace()
		if l.pos >= len(l.b) {
			return nil
		}
		start := l.pos
		if isDigit(l.b[l.pos]) {
			if num, gen, ok := l.objHeader(); ok {
				obj, err := l.next(0, true)
				if err != nil && err != io.EOF && err != ErrMalformed {
					return err
				}
				if dict, ok := obj.(Dict); ok && l.hasKeywordAt("stream") {
					obj = &Stream{Dict: dict, Raw: l.streamData(dict)}
				}
				l.hasKeywordAt("endobj")
				if err := d.define(num, gen, obj, start); err != nil {
					return err
				}
				continue
			}
		}
		tok, err := l.next(0, true)
		if err == io.EOF {
			return nil
		}
		if err == errTooDeep || err == ErrTooLarge {
			return err
		}
		if kw, ok := tok.(keyword); ok {
			switch kw {
			case "xref":
				d.XRefTables++
				if len(d.xrefTables) < maxXRefSections {
					d.xrefTables = append(d.xrefTables, start)
				}
				l.skipXRefTable()
			case "trailer":
				if t, _ := l.next(0, true); t != nil {
					if dict, ok := t.(Dict); ok {
						d.trls = append(d.trls, trailer{dict: dict, order: start})
					}
				}
			case "startxref":
				d.StartXRefs++
				save := l.pos
				d.startXRef = -1
				if n, err := l.next(0, false); err == nil {
					if v, ok := n.(int64); ok && v >= 0 {
						d.startXRef = v
					} else {
						l.pos = save
					}
				}
			}
		}
		if l.pos <= start {
			l.pos = start + 1
		}
	}
}

// objHeader matches "num gen obj" at pos and leaves pos after it.
func (l *lexer) objHeader() (num, gen int, ok bool) {
	save := l.pos
	readInt := func() (int, bool) {
		s := l.pos
		for l.pos < len(l.b) && isDigit(l.b[l.pos]) && l.pos-s < 10 {
			l.pos++
		}
		if l.pos == s || (l.pos < len(l.b) && isRegular(l.b[l.pos])) {
			return 0, false
		}
		n, err := strconv.Atoi(string(l.b[s:l.pos]))
		return n, err == nil
	}
	if num, ok = readInt(); ok {
		l.skipSpace()
		if gen, ok = readInt(); ok && l.hasKeywordAt("obj") {
			return num, gen, true
		}
	}
	l.pos = save
	return 0, 0, false
}

// streamData returns the raw bytes after the "stream" keyword: /Length bytes
// when a direct length checks out, else everything up to "endstream".
func (l *lexer) streamData(dict Dict) []byte {
	// The keyword is followed by CRLF or LF (a lone CR is tolerated).
	if l.pos < len(l.b) && l.b[l.pos] == '\r' {
		l.pos++
	}
	if l.pos < len(l.b) && l.b[l.pos] == '\n' {
		l.pos++
	}
	start := l.pos
	if n, ok := dict["Length"].(int64); ok && n >= 0 && n <= int64(len(l.b)-start) {
		end := start + int(n)
		probe := &lexer{b: l.b, pos: end}
		if probe.hasKeywordAt("endstream") {
			l.pos = probe.pos
			return l.b[start:end]
		}
	}
	i := bytes.Index(l.b[start:], []byte("endstream"))
	if i < 0 {
		l.pos = len(l.b)
		return l.b[start:]
	}
	end := start + i
	l.pos = end + len("endstream")
	raw := l.b[start:end]
	if n := len(raw); n > 0 && raw[n-1] == '\n' {
		raw = raw[:n-1]
		if n := len(raw); n > 0 && raw[n-1] == '\r' {
			raw = raw[:n-1]
		}
	} else if n > 0 && raw[n-1] == '\r' {
		raw = raw[:n-1]
	}
	return raw
}

// skipXRefTable skips the subsections of a classic xref table up to (not
// including) the "trailer" keyword.
func (l *lexer) skipXRefTable() {
	i := bytes.Index(l.b[l.pos:], []byte("trailer"))
	if i < 0 {
		l.pos = len(l.b)
		return
	}
	l.pos += i
}

func (d *Doc) define(num, gen int, obj Object, order int) error {
	if prev, ok := d.objects[num]; ok {
		d.Redefined++
		if prev.order > order {
			return nil
		}
	} else if len(d.objects) >= d.lim.MaxObjects {
		return ErrTooLarge
	}
	d.objects[num] = entry{obj: obj, gen: gen, order: order}
	if s, ok := obj.(*Stream); ok && s.Dict.Name("Type") == "XRef" {
		d.XRefStreams++
		d.trls = append(d.trls, trailer{dict: s.Dict, order: order})
	}
	return nil
}

// expandObjectStreams defines the objects packed in /Type /ObjStm streams.
func (d *Doc) expandObjectStreams() error {
	type objStm struct {
		s     *Stream
		order int
	}
	var list []objStm
	for _, e := range d.objects {
		if s, ok := e.obj.(*Stream); ok && s.Dict.Name("Type") == "ObjStm" {
			list = append(list, objStm{s, e.order})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].order < list[j].order })
	for _, os := range list {
		n, _ := Int(os.s.Dict["N"])
		first, _ := Int(os.s.Dict["First"])
		if n <= 0 || n > int64(d.lim.MaxObjects) || first < 0 {
			continue
		}
		data, err := d.Decode(os.s)
		if err != nil {
			if err == ErrTooLarge {
				return err
			}
			continue // an unreadable object stream leaves its objects undefined
		}
		if first > int64(len(data)) {
			continue
		}
		hl := &lexer{b: data[:first]}
		for i := int64(0); i < n; i++ {
			numTok, err1 := hl.next(0, false)
			offTok, err2 := hl.next(0, false)
			if err1 != nil || err2 != nil {
				break
			}
			num, ok1 := numTok.(int64)
			off, ok2 := offTok.(int64)
			if !ok1 || !ok2 || num < 0 || num > 1<<30 || off < 0 || first+off >= int64(len(data)) {
				continue
			}
			ol := &lexer{b: data, pos: int(first + off)}
			obj, err := ol.next(0, true)
			if err != nil && err != ErrMalformed {
				if err == errTooDeep || err == ErrTooLarge {
					return err
				}
				continue
			}
			if _, isStream := obj.(*Stream); isStream {
				continue
			}
			if err := d.define(int(num), 0, obj, os.order); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeTrailers builds the effective trailer: for each key the value of the
// latest trailer (in file order) that has it.
func (d *Doc) mergeTrailers() {
	sort.SliceStable(d.trls, func(i, j int) bool { return d.trls[i].order < d.trls[j].order })
	d.trailer = Dict{}
	for _, t := range d.trls {
		for k, v := range t.dict {
			d.trailer[k] = v
		}
		if _, ok := t.dict["Prev"]; ok {
			d.HasPrev = true
		}
		if _, ok := t.dict["Encrypt"]; ok {
			d.Encrypted = true
		}
	}
}

// Trailers returns the number of trailer dictionaries (classic or xref stream).
func (d *Doc) Trailers() int { return len(d.trls) }

// Objects returns the number of distinct object numbers.
func (d *Doc) Objects() int { return len(d.objects) }

// Resolve follows indirect references (missing objects resolve to nil).
func (d *Doc) Resolve(o Object) Object {
	for i := 0; i < 32; i++ {
		r, ok := o.(Ref)
		if !ok {
			return o
		}
		e, ok := d.objects[r.Num]
		if !ok {
			return nil
		}
		o = e.obj
	}
	return nil
}

func (d *Doc) dict(o Object) Dict {
	switch v := d.Resolve(o).(type) {
	case Dict:
		return v
	case *Stream:
		return v.Dict
	}
	return nil
}

func (d *Doc) array(o Object) Array {
	a, _ := d.Resolve(o).(Array)
	return a
}

// Root returns the document catalog (nil when missing).
func (d *Doc) Root() Dict { return d.dict(d.trailer["Root"]) }

// Info returns the document information dictionary (nil when missing).
func (d *Doc) Info() Dict { return d.dict(d.trailer["Info"]) }

// InfoString returns a text entry of the information dictionary.
func (d *Doc) InfoString(key Name) (string, bool) {
	info := d.Info()
	if info == nil {
		return "", false
	}
	s, ok := d.Resolve(info[key]).(String)
	if !ok {
		return "", false
	}
	return TextString(s), true
}

// TextString decodes a PDF text string: UTF-16BE with a byte order mark,
// UTF-8 with a BOM (PDF 2.0), else PDFDocEncoding (read as Latin-1).
func TextString(s String) string {
	switch {
	case len(s) >= 2 && s[0] == 0xFE && s[1] == 0xFF:
		u := make([]uint16, 0, (len(s)-2)/2)
		for i := 2; i+1 < len(s); i += 2 {
			u = append(u, uint16(s[i])<<8|uint16(s[i+1]))
		}
		return string(utf16.Decode(u))
	case len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF:
		if utf8.Valid(s[3:]) {
			return string(s[3:])
		}
	}
	r := make([]rune, len(s))
	for i, c := range s {
		r[i] = rune(c)
	}
	return string(r)
}

// Page is one page with its (inherited) resources.
type Page struct {
	Dict      Dict
	Resources Dict
}

// Pages returns the pages in order (at most Limits.MaxPages). Cycles and
// deep trees are cut off.
func (d *Doc) Pages() []Page {
	root := d.Root()
	if root == nil {
		return nil
	}
	var out []Page
	visited := map[Ref]bool{}
	var walk func(o Object, inherited Dict, depth int)
	walk = func(o Object, inherited Dict, depth int) {
		if depth > 32 || len(out) >= d.lim.MaxPages {
			return
		}
		if r, ok := o.(Ref); ok {
			if visited[r] {
				return
			}
			visited[r] = true
		}
		node, ok := d.Resolve(o).(Dict)
		if !ok {
			return
		}
		res := inherited
		if r := d.dict(node["Resources"]); r != nil {
			res = r
		}
		kids := d.array(node["Kids"])
		switch {
		case node.Name("Type") == "Pages" || (node.Name("Type") != "Page" && kids != nil):
			for _, k := range kids {
				walk(k, res, depth+1)
			}
		case node.Name("Type") == "Page" || node["Contents"] != nil:
			out = append(out, Page{Dict: node, Resources: res})
		}
	}
	walk(root["Pages"], nil, 0)
	return out
}

// Annotations returns how many annotations the page carries.
func (d *Doc) Annotations(p Page) int { return len(d.array(p.Dict["Annots"])) }
