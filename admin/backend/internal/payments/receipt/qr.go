package receipt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/yereke99/jantaxi/admin/backend/internal/platform/pdfdoc"
)

// scanQR decodes QR codes in the page images. The first payload's hash is
// kept (unique per receipt in the database); a payload naming a receipt
// number other than the printed one raises FlagQRMismatch. Kaspi receipts
// carry no QR code today, so finding none only raises FlagQRNotFound.
func scanQR(ctx context.Context, doc *pdfdoc.Doc, page pdfdoc.Page, r *Receipt, flags map[string]bool) {
	images, _ := doc.PageImages(page, maxImages)
	var payloads []string
	for _, im := range images {
		if ctx.Err() != nil {
			break
		}
		if p, ok := DecodeQR(im.Img); ok {
			payloads = append(payloads, p)
		}
	}
	if len(payloads) == 0 {
		flags[FlagQRNotFound] = true
		return
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(payloads[0])))
	r.QRPayloadHash = hex.EncodeToString(sum[:])
	for _, p := range payloads {
		for _, s := range findSerials(p) {
			if r.Serial != "" && s != r.Serial {
				flags[FlagQRMismatch] = true
			}
		}
	}
}

// DecodeQR returns the text of a QR code in img. Decoder failures (and any
// panic inside the decoder) report ok=false.
func DecodeQR(img image.Image) (text string, ok bool) {
	defer func() {
		if recover() != nil {
			text, ok = "", false
		}
	}()
	if img == nil {
		return "", false
	}
	b := img.Bounds()
	if b.Dx() < 21 || b.Dy() < 21 || b.Dx()*b.Dy() > limits.MaxImagePixels {
		return "", false
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", false
	}
	hints := map[gozxing.DecodeHintType]any{gozxing.DecodeHintType_TRY_HARDER: true}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil || res == nil {
		return "", false
	}
	t := res.GetText()
	if len(t) > 4096 {
		t = t[:4096]
	}
	return t, t != ""
}
