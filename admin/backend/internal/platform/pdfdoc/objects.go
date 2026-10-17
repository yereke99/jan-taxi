// Package pdfdoc is a small, defensive PDF reader for untrusted uploads.
//
// It finds every object of a file (including objects packed in object
// streams), the trailers and the revision structure, decodes streams within
// byte budgets, extracts text through ToUnicode CMaps and decodes raster
// images. It renders and executes nothing. Every loop and recursion is
// bounded and malformed input yields errors, never panics, so it is safe to
// run on files uploaded by anyone.
//
// The reader is deliberately small: it understands what is needed to read
// machine-generated receipts (fonts with ToUnicode maps, Flate streams with
// PNG predictors, DeviceGray/DeviceRGB/DeviceCMYK/Indexed images, JPEG) and
// reports everything else as unsupported instead of guessing.
package pdfdoc

import (
	"errors"
	"math"
)

// Object is a PDF object: nil (null), bool, int64, float64, String, Name,
// Array, Dict, Ref or *Stream.
type Object any

// Name is a PDF name without the leading slash (#xx escapes resolved).
type Name string

// String is the raw byte content of a literal or hexadecimal string.
type String []byte

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Ref is an indirect reference "num gen R".
type Ref struct{ Num, Gen int }

// Stream is a stream object: its dictionary and the still-encoded bytes.
type Stream struct {
	Dict Dict
	Raw  []byte
}

// keyword is a bare word that is not true/false/null: "obj", "R", content
// stream operators, or garbage.
type keyword string

// endArray and endDict are the closing delimiters returned by the lexer.
type (
	endArray struct{}
	endDict  struct{}
)

// Errors returned by Open and the decoders.
var (
	ErrMalformed   = errors.New("pdfdoc: malformed document")
	ErrTooLarge    = errors.New("pdfdoc: document exceeds the size limits")
	ErrUnsupported = errors.New("pdfdoc: unsupported feature")
	ErrNotPDF      = errors.New("pdfdoc: not a PDF document")
)

// Name returns d[key] when it is a name (after no resolution).
func (d Dict) Name(key Name) Name {
	n, _ := d[key].(Name)
	return n
}

// Int returns o as an integer (reals are truncated when integral enough).
func Int(o Object) (int64, bool) {
	switch v := o.(type) {
	case int64:
		return v, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v > math.MaxInt32 || v < math.MinInt32 {
			return 0, false
		}
		return int64(v), true
	}
	return 0, false
}

// Number returns o as a float64.
func Number(o Object) (float64, bool) {
	switch v := o.(type) {
	case int64:
		return float64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	}
	return 0, false
}
