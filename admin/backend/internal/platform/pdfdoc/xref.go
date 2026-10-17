package pdfdoc

// Bounds of the cross-reference check.
const (
	maxXRefSections = 64
	maxXRefEntries  = 50_000
)

// XRefMismatches checks the cross-reference data against the file and
// returns the number of disagreements: the last startxref does not point at
// a cross-reference section, an in-use entry does not point at the header of
// the object it names, an entry names a missing object stream, or an object
// defined in the file is listed in no section. A writer produces exact
// offsets; changing bytes without rewriting the cross-reference leaves stale
// ones (readers repair them silently, so the file still displays).
func (d *Doc) XRefMismatches() (n int) {
	defer func() {
		if recover() != nil {
			n++
		}
	}()
	listed := map[int]bool{}
	entries := 0
	check := func(num int, offset int64) {
		listed[num] = true
		if offset < 0 || offset >= int64(len(d.data)) {
			n++
			return
		}
		l := &lexer{b: d.data, pos: int(offset)}
		if got, _, ok := l.objHeader(); !ok || got != num {
			n++
		}
	}

	// Classic tables: "start count" subsections of "offset gen n|f" rows.
	for _, pos := range d.xrefTables {
		l := &lexer{b: d.data, pos: pos + len("xref")}
	sections:
		for entries < maxXRefEntries {
			first, ok1 := l.int()
			count, ok2 := l.int()
			if !ok1 || !ok2 || first < 0 || count < 0 || count > maxXRefEntries {
				break
			}
			for i := int64(0); i < count; i++ {
				off, ok1 := l.int()
				_, ok2 := l.int()
				kw, _ := l.next(0, false)
				if !ok1 || !ok2 {
					n++
					break sections
				}
				if entries++; entries > maxXRefEntries {
					break sections
				}
				switch kw {
				case keyword("n"):
					check(int(first+i), off)
				case keyword("f"):
				default:
					n++
					break sections
				}
			}
		}
	}

	// Cross-reference streams: /W field widths, /Index subsections.
	streamAt := map[int64]bool{}
	for num, e := range d.objects {
		s, ok := e.obj.(*Stream)
		if !ok || s.Dict.Name("Type") != "XRef" {
			continue
		}
		streamAt[int64(e.order)] = true
		listed[num] = true
		w := d.array(s.Dict["W"])
		if len(w) != 3 {
			n++
			continue
		}
		var widths [3]int
		row := 0
		for i := range widths {
			v, ok := Int(d.Resolve(w[i]))
			if !ok || v < 0 || v > 8 {
				n++
				row = -1
				break
			}
			widths[i] = int(v)
			row += int(v)
		}
		if row <= 0 {
			continue
		}
		data, err := d.Decode(s)
		if err != nil {
			n++
			continue
		}
		index := d.array(s.Dict["Index"])
		if index == nil {
			size, _ := Int(d.Resolve(s.Dict["Size"]))
			index = Array{int64(0), size}
		}
		p := 0
		for k := 0; k+1 < len(index) && entries < maxXRefEntries; k += 2 {
			first, ok1 := Int(d.Resolve(index[k]))
			count, ok2 := Int(d.Resolve(index[k+1]))
			if !ok1 || !ok2 || first < 0 || count < 0 {
				n++
				break
			}
			for i := int64(0); i < count && entries < maxXRefEntries; i++ {
				if p+row > len(data) {
					n++
					break
				}
				f := [3]int64{1, 0, 0} // type 1 when the type field is absent
				for j, wd := range widths {
					if wd == 0 {
						continue
					}
					var v int64
					for _, c := range data[p : p+wd] {
						v = v<<8 | int64(c)
					}
					f[j] = v
					p += wd
				}
				entries++
				num := int(first + i)
				switch f[0] {
				case 1:
					check(num, f[1])
				case 2:
					listed[num] = true
					os, ok := d.Resolve(Ref{Num: int(f[1])}).(*Stream)
					if !ok || os.Dict.Name("Type") != "ObjStm" {
						n++
						break
					}
					if count, _ := Int(d.Resolve(os.Dict["N"])); f[2] >= count {
						n++
					}
				}
			}
		}
	}

	// startxref names the last section written.
	switch {
	case d.startXRef < 0:
		n++
	case streamAt[d.startXRef]:
	default:
		l := &lexer{b: d.data, pos: int(min(d.startXRef, int64(len(d.data))))}
		if kw, _ := l.next(0, false); kw != keyword("xref") {
			n++
		}
	}

	for num := range d.objects {
		if !listed[num] {
			n++
		}
	}
	return n
}

// int reads one non-negative integer token.
func (l *lexer) int() (int64, bool) {
	o, err := l.next(0, false)
	if err != nil {
		return 0, false
	}
	v, ok := o.(int64)
	return v, ok && v >= 0
}
