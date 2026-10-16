package storage

import (
	"bytes"
	"encoding/binary"
)

// jpegOrientation returns the EXIF Orientation tag (1..8) of a JPEG, or 1 when
// absent or unreadable. Only the APP1 "Exif" segment's IFD0 is inspected, with
// bounds checks on every read: the input is untrusted.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		i += 2
		switch {
		case marker == 0xFF: // fill byte
			i--
			continue
		case marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			continue // markers without a length
		case marker == 0xDA || marker == 0xD9: // start of scan / end of image
			return 1
		}
		if i+2 > len(b) {
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(b[i : i+2]))
		if segLen < 2 || i+segLen > len(b) {
			return 1
		}
		seg := b[i+2 : i+segLen]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
		i += segLen
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:4]) != 42 {
		return 1
	}
	ifd := int(bo.Uint32(t[4:8]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd : ifd+2]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		tag := bo.Uint16(t[e : e+2])
		typ := bo.Uint16(t[e+2 : e+4])
		if tag != 0x0112 {
			continue
		}
		if typ != 3 { // SHORT
			return 1
		}
		v := int(bo.Uint16(t[e+8 : e+10]))
		if v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}
