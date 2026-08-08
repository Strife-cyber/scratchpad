package browser

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// syntheticJPEG renders a noisy image large enough that encoding produces a
// multi-kilobyte JPEG. Pure-in-memory, so the test never touches a browser.
func syntheticJPEG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Random-ish gradient gives JPEG entropy so the buffer is actually large.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x * 13) % 256),
				G: uint8((y * 17) % 256),
				B: uint8((x + y) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode synthetic jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestDownscaleJPEG_UnderBudget(t *testing.T) {
	buf := syntheticJPEG(t, 900, 600)
	if len(buf) < 5000 {
		t.Fatalf("synthetic jpeg too small to test: %d bytes", len(buf))
	}

	out := downscaleJPEG(buf, 4000)
	if len(out) > 4000 {
		t.Errorf("downscaled size %d exceeds budget 4000", len(out))
	}
	if len(out) == 0 {
		t.Fatal("downscale produced empty output")
	}
	// Output must still decode as a valid JPEG.
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("downscaled output is not a valid JPEG: %v", err)
	}
}

func TestDownscaleJPEG_AlreadyFitsIsUnchanged(t *testing.T) {
	buf := syntheticJPEG(t, 200, 150)
	out := downscaleJPEG(buf, len(buf)+1)
	if !bytes.Equal(out, buf) {
		t.Error("over-budget input must be returned unchanged")
	}
}

func TestDownscaleJPEG_NoBudgetIsUnchanged(t *testing.T) {
	buf := syntheticJPEG(t, 800, 500)
	if got := downscaleJPEG(buf, 0); !bytes.Equal(got, buf) {
		t.Error("maxBytes=0 must not alter the buffer")
	}
}

func TestBoxSample_ShrinksDimensions(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 400, 300))
	dst := boxSample(src, 100, 75)
	b := dst.Bounds()
	if b.Dx() != 100 || b.Dy() != 75 {
		t.Errorf("boxSample dims = %dx%d, want 100x75", b.Dx(), b.Dy())
	}
}

func TestBoxSample_UpscaleFloor(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 20, 20))
	dst := boxSample(src, 1, 1)
	b := dst.Bounds()
	if b.Dx() != 1 || b.Dy() != 1 {
		t.Errorf("boxSample floor dims = %dx%d, want 1x1", b.Dx(), b.Dy())
	}
}
