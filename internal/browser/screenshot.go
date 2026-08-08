package browser

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
)

// downscaleJPEG re-encodes a JPEG so its encoded size fits under maxBytes,
// using manual box-sampling (improvement-plan item 36.2). It progressively
// shrinks the image and re-encodes at reduced quality until the budget is met.
// The original buffer is returned unchanged when it already fits, when no
// budget is set, or when the image cannot be decoded. If the budget can never
// be met (e.g. an extreme floor), the smallest attempt is returned.
func downscaleJPEG(buf []byte, maxBytes int) []byte {
	if maxBytes <= 0 || len(buf) <= maxBytes {
		return buf
	}
	img, err := jpeg.Decode(bytes.NewReader(buf))
	if err != nil {
		return buf
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return buf
	}

	best := buf
	scale := 1.0
	for i := 0; i < 10; i++ {
		scale *= 0.72
		nw := max(1, int(float64(w)*scale))
		nh := max(1, int(float64(h)*scale))
		var sb bytes.Buffer
		if err := jpeg.Encode(&sb, boxSample(img, nw, nh), &jpeg.Options{Quality: 65}); err != nil {
			return best
		}
		out := sb.Bytes()
		if len(out) <= maxBytes {
			return out
		}
		if len(out) < len(best) {
			best = out
		}
	}
	return best
}

// boxSample downsamples src to newW×newH by averaging each destination pixel's
// source block (box filtering). Pure-stdlib, so no image/draw dependency.
func boxSample(src image.Image, newW, newH int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		sy0 := y * sh / newH
		sy1 := (y + 1) * sh / newH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < newW; x++ {
			sx0 := x * sw / newW
			sx1 := (x + 1) * sw / newW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, a int64
			count := 0
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r += int64(cr)
					g += int64(cg)
					bl += int64(cb)
					a += int64(ca)
					count++
				}
			}
			if count > 0 {
				dst.SetRGBA(x, y, color.RGBA{
					R: uint8((r / int64(count)) >> 8),
					G: uint8((g / int64(count)) >> 8),
					B: uint8((bl / int64(count)) >> 8),
					A: uint8((a / int64(count)) >> 8),
				})
			}
		}
	}
	return dst
}
