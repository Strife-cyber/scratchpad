package browser

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math/bits"
)

// dhash compares two images using a dHash-style perceptual hash.
// It returns the Hamming distance (0 means identical).
func dhashDistance(a, b image.Image) int {
	// dHash uses an 9x8 grayscale image.
	// We avoid external deps by using nearest-neighbor sampling.
	const w = 9
	const h = 8

	ga := grayscaleSample(a, w, h)
	gb := grayscaleSample(b, w, h)

	// Build 64-bit hashes: compare adjacent pixels horizontally.
	var ha uint64
	var hb uint64
	for y := 0; y < h; y++ {
		for x := 0; x < w-1; x++ {
			la := ga[y][x]
			lb := ga[y][x+1]
			if la < lb {
				ha |= 1 << uint64(y*(w-1)+x)
			}

			lla := gb[y][x]
			llb := gb[y][x+1]
			if lla < llb {
				hb |= 1 << uint64(y*(w-1)+x)
			}
		}
	}
	return bits.OnesCount64(ha ^ hb)
}

func grayscaleSample(img image.Image, w, h int) [][]uint8 {
	out := make([][]uint8, h)
	for y := 0; y < h; y++ {
		out[y] = make([]uint8, w)
		for x := 0; x < w; x++ {
			// Map target pixel into source space.
			sx := img.Bounds().Min.X + x*(img.Bounds().Dx())/w
			sy := img.Bounds().Min.Y + y*(img.Bounds().Dy())/h
			r, g, b, _ := img.At(sx, sy).RGBA()
			// Convert to luminance (0..255).
			l := uint8((299*r + 587*g + 114*b + 500) / 1000 >> 8)
			out[y][x] = l
		}
	}
	return out
}

// perceptualMatch compares base64-encoded screenshots using dHash.
func perceptualMatch(actualJpegBytes, expectedJpegBytes []byte, tolerance int) (success bool, distance int, err error) {
	aImg, _, err := image.Decode(bytes.NewReader(actualJpegBytes))
	if err != nil {
		return false, 0, err
	}
	bImg, _, err := image.Decode(bytes.NewReader(expectedJpegBytes))
	if err != nil {
		return false, 0, err
	}
	d := dhashDistance(aImg, bImg)
	return d <= tolerance, d, nil
}
