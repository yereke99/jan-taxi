package pdfdoc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"sort"
)

// Image is a decoded image XObject of a page.
type Image struct {
	Name          Name // resource name
	Width, Height int
	Img           image.Image
}

// ImageInfo describes an image XObject without decoding it.
type ImageInfo struct {
	Name          Name
	Width, Height int
	Filter        string
}

// maxImageSide bounds either dimension of a decoded image.
const maxImageSide = 8192

// PageImages decodes up to limit image XObjects reachable from the page
// resources (also through Form XObjects, up to a small depth). Images that
// cannot be decoded are listed in skipped instead.
func (d *Doc) PageImages(p Page, limit int) (images []Image, skipped []ImageInfo) {
	visited := map[Ref]bool{}
	var walk func(res Dict, depth int)
	walk = func(res Dict, depth int) {
		if depth > 4 || res == nil {
			return
		}
		xobjs := d.dict(res["XObject"])
		names := make([]string, 0, len(xobjs))
		for n := range xobjs {
			names = append(names, string(n))
		}
		sort.Strings(names) // deterministic order
		for _, n := range names {
			if len(images)+len(skipped) >= limit {
				return
			}
			o := xobjs[Name(n)]
			if r, ok := o.(Ref); ok {
				if visited[r] {
					continue
				}
				visited[r] = true
			}
			s, ok := d.Resolve(o).(*Stream)
			if !ok {
				continue
			}
			switch s.Dict.Name("Subtype") {
			case "Image":
				img, info, err := d.decodeImage(s, 0)
				info.Name = Name(n)
				if err != nil {
					skipped = append(skipped, info)
					continue
				}
				images = append(images, Image{Name: Name(n), Width: info.Width, Height: info.Height, Img: img})
			case "Form":
				walk(d.dict(s.Dict["Resources"]), depth+1)
			}
		}
	}
	walk(p.Resources, 0)
	return images, skipped
}

func (d *Doc) decodeImage(s *Stream, depth int) (image.Image, ImageInfo, error) {
	w, _ := Int(d.Resolve(s.Dict["Width"]))
	h, _ := Int(d.Resolve(s.Dict["Height"]))
	names, parms := d.filterList(s)
	info := ImageInfo{Width: int(w), Height: int(h)}
	if len(names) > 0 {
		info.Filter = string(names[len(names)-1])
	}
	if w <= 0 || h <= 0 || w > maxImageSide || h > maxImageSide || w*h > int64(d.lim.MaxImagePixels) {
		return nil, info, ErrTooLarge
	}
	if len(names) > 0 && (names[len(names)-1] == "DCTDecode" || names[len(names)-1] == "DCT") {
		data, err := d.decodeFilters(s.Raw, names[:len(names)-1], parms[:len(names)-1])
		if err != nil {
			return nil, info, err
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > int64(d.lim.MaxImagePixels) {
			return nil, info, ErrUnsupported
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, info, ErrMalformed
		}
		return img, info, nil
	}
	data, err := d.Decode(s)
	if err != nil {
		return nil, info, err
	}
	width, height := int(w), int(h)
	if mask, _ := d.Resolve(s.Dict["ImageMask"]).(bool); mask {
		return bitImage(data, width, height)
	}
	bpc, _ := Int(d.Resolve(s.Dict["BitsPerComponent"]))
	cs := d.Resolve(s.Dict["ColorSpace"])
	img, err := d.samples(data, width, height, int(bpc), cs)
	if err != nil {
		return nil, info, err
	}
	if sm, ok := d.Resolve(s.Dict["SMask"]).(*Stream); ok && depth == 0 {
		if alpha, _, err := d.decodeImage(sm, depth+1); err == nil && alpha.Bounds() == img.Bounds() {
			img = flatten(img, alpha)
		}
	}
	return img, info, nil
}

// bitImage decodes 1-bit samples: 1 is white, 0 is black (for DeviceGray
// samples and for stencil masks with the default Decode array alike).
func bitImage(data []byte, w, h int) (image.Image, ImageInfo, error) {
	info := ImageInfo{Width: w, Height: h}
	rowLen := (w + 7) / 8
	if len(data) < rowLen*h {
		return nil, info, ErrMalformed
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := data[y*rowLen : (y+1)*rowLen]
		for x := 0; x < w; x++ {
			if row[x/8]>>(7-uint(x%8))&1 == 1 {
				img.Pix[y*img.Stride+x] = 0xFF
			}
		}
	}
	return img, info, nil
}

// samples decodes 8-bit (or 1-bit gray) samples in DeviceGray, DeviceRGB,
// DeviceCMYK, ICCBased or Indexed color spaces.
func (d *Doc) samples(data []byte, w, h, bpc int, cs Object) (image.Image, error) {
	space, comps, palette, err := d.colorSpace(cs, 0)
	if err != nil {
		return nil, err
	}
	if bpc == 1 && space == "DeviceGray" {
		img, _, err := bitImage(data, w, h)
		return img, err
	}
	if bpc != 8 {
		return nil, ErrUnsupported
	}
	if len(data) < w*h*comps {
		return nil, ErrMalformed
	}
	rect := image.Rect(0, 0, w, h)
	switch space {
	case "DeviceGray":
		img := image.NewGray(rect)
		copy(img.Pix, data[:w*h])
		return img, nil
	case "DeviceRGB":
		img := image.NewRGBA(rect)
		for i := 0; i < w*h; i++ {
			img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = data[i*3], data[i*3+1], data[i*3+2], 0xFF
		}
		return img, nil
	case "DeviceCMYK":
		img := image.NewRGBA(rect)
		for i := 0; i < w*h; i++ {
			r, g, b := color.CMYKToRGB(data[i*4], data[i*4+1], data[i*4+2], data[i*4+3])
			img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = r, g, b, 0xFF
		}
		return img, nil
	case "Indexed":
		img := image.NewRGBA(rect)
		for i := 0; i < w*h; i++ {
			c := color.RGBA{A: 0xFF}
			if idx := int(data[i]); idx < len(palette) {
				c = palette[idx]
			}
			img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = c.R, c.G, c.B, 0xFF
		}
		return img, nil
	}
	return nil, ErrUnsupported
}

// colorSpace resolves a color space to a device family, its component count
// and, for Indexed, the palette.
func (d *Doc) colorSpace(cs Object, depth int) (string, int, []color.RGBA, error) {
	if depth > 3 {
		return "", 0, nil, ErrUnsupported
	}
	switch v := d.Resolve(cs).(type) {
	case Name:
		switch v {
		case "DeviceGray", "G", "CalGray":
			return "DeviceGray", 1, nil, nil
		case "DeviceRGB", "RGB", "CalRGB":
			return "DeviceRGB", 3, nil, nil
		case "DeviceCMYK", "CMYK":
			return "DeviceCMYK", 4, nil, nil
		}
	case Array:
		if len(v) == 0 {
			break
		}
		kind, _ := d.Resolve(v[0]).(Name)
		switch kind {
		case "ICCBased":
			if len(v) > 1 {
				if s, ok := d.Resolve(v[1]).(*Stream); ok {
					n, _ := Int(d.Resolve(s.Dict["N"]))
					switch n {
					case 1:
						return "DeviceGray", 1, nil, nil
					case 3:
						return "DeviceRGB", 3, nil, nil
					case 4:
						return "DeviceCMYK", 4, nil, nil
					}
				}
			}
		case "CalGray":
			return "DeviceGray", 1, nil, nil
		case "CalRGB":
			return "DeviceRGB", 3, nil, nil
		case "Indexed", "I":
			if len(v) < 4 {
				break
			}
			base, comps, _, err := d.colorSpace(v[1], depth+1)
			if err != nil || base == "Indexed" {
				return "", 0, nil, ErrUnsupported
			}
			hival, _ := Int(d.Resolve(v[2]))
			if hival < 0 || hival > 255 {
				return "", 0, nil, ErrUnsupported
			}
			var lookup []byte
			switch t := d.Resolve(v[3]).(type) {
			case String:
				lookup = t
			case *Stream:
				b, err := d.Decode(t)
				if err != nil {
					return "", 0, nil, err
				}
				lookup = b
			}
			pal := make([]color.RGBA, 0, hival+1)
			for i := 0; i <= int(hival) && (i+1)*comps <= len(lookup); i++ {
				e := lookup[i*comps : (i+1)*comps]
				switch base {
				case "DeviceGray":
					pal = append(pal, color.RGBA{e[0], e[0], e[0], 0xFF})
				case "DeviceRGB":
					pal = append(pal, color.RGBA{e[0], e[1], e[2], 0xFF})
				case "DeviceCMYK":
					r, g, b := color.CMYKToRGB(e[0], e[1], e[2], e[3])
					pal = append(pal, color.RGBA{r, g, b, 0xFF})
				}
			}
			return "Indexed", 1, pal, nil
		}
	}
	return "", 0, nil, ErrUnsupported
}

// flatten composites img over white using alpha (a gray image of the same size).
func flatten(img, alpha image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			a, _, _, _ := alpha.At(x, y).RGBA()
			mix := func(c uint32) uint8 { return uint8((c*a + 0xFFFF*(0xFFFF-a)) / 0xFFFF >> 8) }
			i := out.PixOffset(x, y)
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = mix(r), mix(g), mix(bl), 0xFF
		}
	}
	return out
}
