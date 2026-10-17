package pdfdoc

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"io"
)

// filterList returns the stream's filters and their parameters.
func (d *Doc) filterList(s *Stream) ([]Name, []Dict) {
	var names []Name
	var parms []Dict
	switch f := d.Resolve(s.Dict["Filter"]).(type) {
	case Name:
		names = []Name{f}
	case Array:
		for _, o := range f {
			n, _ := d.Resolve(o).(Name)
			names = append(names, n)
		}
	}
	p := s.Dict["DecodeParms"]
	if p == nil {
		p = s.Dict["DP"]
	}
	switch v := d.Resolve(p).(type) {
	case Dict:
		parms = []Dict{v}
	case Array:
		for _, o := range v {
			pd, _ := d.Resolve(o).(Dict)
			parms = append(parms, pd)
		}
	}
	for len(parms) < len(names) {
		parms = append(parms, nil)
	}
	return names, parms
}

// Decode returns the decoded bytes of s. Supported filters: FlateDecode
// (with PNG and TIFF predictors), ASCIIHexDecode and ASCII85Decode. Image
// codecs (DCTDecode) are handled by the image decoder. Decoded bytes count
// against the document budget.
func (d *Doc) Decode(s *Stream) ([]byte, error) {
	names, parms := d.filterList(s)
	return d.decodeFilters(s.Raw, names, parms)
}

func (d *Doc) decodeFilters(data []byte, names []Name, parms []Dict) ([]byte, error) {
	if len(names) > 4 {
		return nil, ErrUnsupported
	}
	for i, n := range names {
		var err error
		limit := min(d.lim.MaxStreamBytes, d.budget)
		switch n {
		case "FlateDecode", "Fl":
			data, err = inflate(data, limit)
			if err == nil {
				data, err = d.predict(data, parms[i])
			}
		case "ASCIIHexDecode", "AHx":
			data, err = asciiHex(data)
		case "ASCII85Decode", "A85":
			data, err = ascii85(data)
		default:
			return nil, ErrUnsupported
		}
		if err != nil {
			return nil, err
		}
		if len(data) > limit {
			return nil, ErrTooLarge
		}
	}
	d.budget -= len(data)
	if d.budget < 0 {
		return nil, ErrTooLarge
	}
	return data, nil
}

// inflate decompresses zlib (or raw deflate) data, reading at most limit+1
// bytes so a compression bomb cannot exhaust memory. A damaged tail is
// tolerated when some data came out.
func inflate(src []byte, limit int) ([]byte, error) {
	var r io.Reader
	if zr, err := zlib.NewReader(bytes.NewReader(src)); err == nil {
		defer zr.Close()
		r = zr
	} else {
		fr := flate.NewReader(bytes.NewReader(src))
		defer fr.Close()
		r = fr
	}
	out, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if len(out) > limit {
		return nil, ErrTooLarge
	}
	if err != nil && len(out) == 0 {
		return nil, ErrMalformed
	}
	return out, nil
}

func asciiHex(src []byte) ([]byte, error) {
	out := make([]byte, 0, len(src)/2)
	var hi byte
	half := false
	for _, c := range src {
		if c == '>' {
			break
		}
		v, ok := unhex(c)
		if !ok {
			if isWhite(c) {
				continue
			}
			return nil, ErrMalformed
		}
		if half {
			out = append(out, hi<<4|v)
		} else {
			hi = v
		}
		half = !half
	}
	if half {
		out = append(out, hi<<4)
	}
	return out, nil
}

func ascii85(src []byte) ([]byte, error) {
	out := make([]byte, 0, len(src))
	var group [5]byte
	n := 0
	flush := func(count int) {
		var v uint32
		for i := 0; i < 5; i++ {
			c := byte('u')
			if i < count {
				c = group[i]
			}
			v = v*85 + uint32(c-'!')
		}
		b := [4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		out = append(out, b[:count-1]...)
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case isWhite(c):
			continue
		case c == '~':
			if n > 1 {
				flush(n)
			}
			return out, nil
		case c == 'z' && n == 0:
			out = append(out, 0, 0, 0, 0)
		case c >= '!' && c <= 'u':
			group[n] = c
			n++
			if n == 5 {
				flush(5)
				n = 0
			}
		default:
			return nil, ErrMalformed
		}
	}
	if n > 1 {
		flush(n)
	}
	return out, nil
}

// predict undoes a Flate predictor (TIFF 2 for 8-bit samples, PNG 10–15).
func (d *Doc) predict(data []byte, parms Dict) ([]byte, error) {
	if parms == nil {
		return data, nil
	}
	pred, _ := Int(d.Resolve(parms["Predictor"]))
	if pred <= 1 {
		return data, nil
	}
	colors := int64(1)
	if v, ok := Int(d.Resolve(parms["Colors"])); ok {
		colors = v
	}
	bpc := int64(8)
	if v, ok := Int(d.Resolve(parms["BitsPerComponent"])); ok {
		bpc = v
	}
	columns := int64(1)
	if v, ok := Int(d.Resolve(parms["Columns"])); ok {
		columns = v
	}
	if colors < 1 || colors > 32 || columns < 1 || columns > 1<<20 {
		return nil, ErrUnsupported
	}
	switch bpc {
	case 1, 2, 4, 8, 16:
	default:
		return nil, ErrUnsupported
	}
	rowLen := int((colors*bpc*columns + 7) / 8)
	bpp := max(1, int(colors*bpc/8))
	if pred == 2 {
		if bpc != 8 {
			return nil, ErrUnsupported
		}
		out := append([]byte(nil), data...)
		for r := 0; r+rowLen <= len(out); r += rowLen {
			row := out[r : r+rowLen]
			for i := bpp; i < len(row); i++ {
				row[i] += row[i-bpp]
			}
		}
		return out, nil
	}
	if pred < 10 || pred > 15 {
		return nil, ErrUnsupported
	}
	stride := rowLen + 1
	rows := len(data) / stride
	out := make([]byte, rows*rowLen)
	prev := make([]byte, rowLen)
	for r := 0; r < rows; r++ {
		src := data[r*stride : r*stride+stride]
		ft, in := src[0], src[1:]
		cur := out[r*rowLen : (r+1)*rowLen]
		for i := 0; i < rowLen; i++ {
			var a, c byte
			if i >= bpp {
				a = cur[i-bpp]
				c = prev[i-bpp]
			}
			b := prev[i]
			switch ft {
			case 0:
				cur[i] = in[i]
			case 1:
				cur[i] = in[i] + a
			case 2:
				cur[i] = in[i] + b
			case 3:
				cur[i] = in[i] + byte((int(a)+int(b))/2)
			case 4:
				cur[i] = in[i] + paeth(a, b, c)
			default:
				return nil, ErrMalformed
			}
		}
		prev = cur
	}
	return out, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
