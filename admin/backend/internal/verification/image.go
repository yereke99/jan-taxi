package verification

import (
	"context"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"os"
	"strconv"

	"golang.org/x/image/draw"
)

// cardRegions locates one or two landscape cards on a light page or phone
// screenshot. Colour is used only for image preparation, never as evidence of
// authenticity. Ambiguous layouts keep the complete image. This removes viewer
// chrome/large margins before OCR, without consulting the entered identity.
func cardRegions(src image.Image) []image.Rectangle {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 150 || h < 100 {
		return nil
	}
	type row struct{ left, right int }
	rows := make([]row, h)
	for y := range h {
		left, right, n := w, 0, 0
		for x := 0; x < w; x += 2 {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			hi, lo := max(r, g, bl)>>8, min(r, g, bl)>>8
			if hi > 120 && lo < 245 && hi-lo >= 18 {
				left, right, n = min(left, x), max(right, x+2), n+2
			}
		}
		if n >= w*12/100 {
			rows[y] = row{left, min(right, w)}
		}
	}
	var regions []image.Rectangle
	gap := max(3, h/400)
	for y := 0; y < h; {
		if rows[y].right == 0 {
			y++
			continue
		}
		start, end, left, right := y, y+1, rows[y].left, rows[y].right
		for y++; y < h && y-end <= gap; y++ {
			if rows[y].right > 0 {
				end, left, right = y+1, min(left, rows[y].left), max(right, rows[y].right)
			}
		}
		cw, ch := right-left, end-start
		if cw < max(150, w/3) || ch < 80 || float64(cw)/float64(ch) < 1.2 || float64(cw)/float64(ch) > 3.3 {
			continue
		}
		// On the reverse, only the upper band may be coloured; the MRZ is often
		// nearly white. Keep the complete card-height area below that band.
		end = min(h, max(end, start+cw*1000/1586))
		pad := max(8, cw/60)
		r := image.Rect(max(0, left-pad), max(0, start-pad), min(w, right+pad), min(h, end+pad)).Add(b.Min)
		regions = append(regions, r)
	}
	if len(regions) > 2 {
		return nil
	}
	return regions
}

// prepareCards creates temporary 2000px card images sequentially. Original
// uploads are never changed. Decoding stays within the API/raster size bound.
func prepareCards(ctx context.Context, path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &processingError{Stage: "image_prepare", Code: "unavailable"}
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 4_000_000 {
		f.Close()
		return nil, nil // older images still go through the bounded OCR process
	}
	if _, err = f.Seek(0, 0); err != nil {
		f.Close()
		return nil, &processingError{Stage: "image_prepare", Code: "read_failed"}
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return nil, &processingError{Stage: "image_prepare", Code: "decode_failed"}
	}
	regions := cardRegions(src)
	paths := make([]string, 0, len(regions))
	for i, r := range regions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		const side = 2000
		w, h := side, max(1, r.Dy()*side/r.Dx())
		if h > side {
			w, h = max(1, r.Dx()*side/r.Dy()), side
		}
		dst := image.NewRGBA(image.Rect(0, 0, w+20, h+20))
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.CatmullRom.Scale(dst, image.Rect(10, 10, w+10, h+10), src, r, draw.Src, nil)
		p := path + "-card-" + strconv.Itoa(i) + ".png"
		out, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, &processingError{Stage: "image_prepare", Code: "write_failed"}
		}
		err = png.Encode(out, dst)
		closeErr := out.Close()
		if err != nil || closeErr != nil {
			return nil, &processingError{Stage: "image_prepare", Code: "write_failed"}
		}
		paths = append(paths, p)
	}
	return paths, nil
}
