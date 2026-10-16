package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Image limits.
const (
	MaxPixels   = 40_000_000 // decoded size limit checked before full decoding
	MaxSide     = 2000       // longest side of the stored image
	JPEGQuality = 85
)

// decodeSlots bounds concurrent decodes: a 40 MP image needs up to ~160 MB
// while decoded, so a burst of uploads must not exhaust memory.
var decodeSlots = make(chan struct{}, 2)

type format int

const (
	formatUnknown format = iota
	formatJPEG
	formatPNG
	formatWebP
)

// sniff identifies the image format by magic bytes only; file names and
// client-declared content types are ignored.
func sniff(b []byte) format {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return formatJPEG
	case len(b) >= 8 && bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")):
		return formatPNG
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return formatWebP
	}
	return formatUnknown
}

type sanitized struct {
	data          []byte
	width, height int
	sha256        string
}

// sanitize decodes the upload, applies the EXIF orientation, bounds the size,
// flattens transparency onto white and re-encodes as baseline JPEG. Nothing
// from the original container (EXIF, XMP, ICC, comments) survives.
func sanitize(ctx context.Context, data []byte) (sanitized, error) {
	f := sniff(data)
	if f == formatUnknown {
		return sanitized{}, ErrUnsupportedType
	}
	cfg, err := decodeConfig(f, data)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return sanitized{}, ErrUnsupportedType
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return sanitized{}, ErrImageTooLarge
	}

	select {
	case decodeSlots <- struct{}{}:
		defer func() { <-decodeSlots }()
	case <-ctx.Done():
		return sanitized{}, ctx.Err()
	}

	src, err := decode(f, data)
	if err != nil {
		return sanitized{}, ErrUnsupportedType
	}
	orientation := 1
	if f == formatJPEG {
		orientation = jpegOrientation(data)
	}

	img := fitAndFlatten(src, MaxSide)
	if orientation != 1 {
		img = orient(toRGBA(img), orientation)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return sanitized{}, err
	}
	sum := sha256.Sum256(buf.Bytes())
	b := img.Bounds()
	return sanitized{data: buf.Bytes(), width: b.Dx(), height: b.Dy(), sha256: hex.EncodeToString(sum[:])}, nil
}

func decodeConfig(f format, data []byte) (image.Config, error) {
	r := bytes.NewReader(data)
	switch f {
	case formatJPEG:
		return jpeg.DecodeConfig(r)
	case formatPNG:
		return png.DecodeConfig(r)
	default:
		return webp.DecodeConfig(r)
	}
}

func decode(f format, data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	switch f {
	case formatJPEG:
		return jpeg.Decode(r)
	case formatPNG:
		return png.Decode(r)
	default:
		return webp.Decode(r)
	}
}

// fitSize scales (w, h) so that the longest side is at most max.
func fitSize(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, clampMin1(int(math.Round(float64(h) * float64(max) / float64(w))))
	}
	return clampMin1(int(math.Round(float64(w) * float64(max) / float64(h)))), max
}

func clampMin1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

type opaquer interface{ Opaque() bool }

func isOpaque(img image.Image) bool {
	if o, ok := img.(opaquer); ok {
		return o.Opaque()
	}
	return false
}

// fitAndFlatten downsizes src to fit max×max (Catmull-Rom, which keeps
// document text legible) and composites any transparency onto white, since
// JPEG has no alpha channel.
func fitAndFlatten(src image.Image, max int) image.Image {
	sb := src.Bounds()
	w, h := fitSize(sb.Dx(), sb.Dy(), max)
	opaque := isOpaque(src)
	if w == sb.Dx() && h == sb.Dy() && opaque {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	op := draw.Src
	if !opaque {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		op = draw.Over
	}
	if w == sb.Dx() && h == sb.Dy() {
		draw.Draw(dst, dst.Bounds(), src, sb.Min, op)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, sb, op, nil)
	}
	return dst
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// orient applies an EXIF orientation (2..8) so the stored image displays
// upright without metadata. For each destination pixel (x, y) it reads the
// source pixel given by the standard EXIF transform.
func orient(src *image.RGBA, o int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if o >= 5 && o <= 8 {
		dw, dh = h, w
	}
	var at func(x, y int) (int, int)
	switch o {
	case 2: // mirror horizontally
		at = func(x, y int) (int, int) { return w - 1 - x, y }
	case 3: // rotate 180
		at = func(x, y int) (int, int) { return w - 1 - x, h - 1 - y }
	case 4: // mirror vertically
		at = func(x, y int) (int, int) { return x, h - 1 - y }
	case 5: // transpose
		at = func(x, y int) (int, int) { return y, x }
	case 6: // rotate 90 clockwise
		at = func(x, y int) (int, int) { return y, h - 1 - x }
	case 7: // transverse
		at = func(x, y int) (int, int) { return w - 1 - y, h - 1 - x }
	case 8: // rotate 90 counter-clockwise
		at = func(x, y int) (int, int) { return w - 1 - y, x }
	default:
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		row := dst.Pix[y*dst.Stride : y*dst.Stride+dw*4]
		for x := 0; x < dw; x++ {
			sx, sy := at(x, y)
			si := sy*src.Stride + sx*4
			copy(row[x*4:x*4+4], src.Pix[si:si+4])
		}
	}
	return dst
}
