package gaf

import (
	"bytes"
	"image"
	"testing"
)

// divergentSequence builds a 2-frame sequence whose frames resolve DIFFERENT
// transparent indices, mirroring TA: Kingdoms uncompressed atlases:
//
//   - Frame 0: TransparencyIndex 9 is present in the pixel data, so it resolves
//     to 9 — this becomes the animation-wide global transparent index.
//   - Frame 1: TransparencyIndex 9 is absent from the data but all four corners
//     are 0, so the corner heuristic resolves it to 0. Its transparent
//     background (index 0) differs from the global index (9).
func divergentSequence() *Sequence {
	f0 := make([]byte, 16)
	for i := range f0 {
		f0[i] = 9
	}
	f0[5] = 200 // a body pixel so the frame isn't uniform
	frame0 := &Frame{Width: 4, Height: 4, TransparencyIndex: 9, Duration: 3, Pixels: f0}

	f1 := make([]byte, 16) // all zero: corners are 0, index 9 absent
	f1[5] = 200
	f1[6] = 201
	frame1 := &Frame{Width: 4, Height: 4, TransparencyIndex: 9, Duration: 3, Pixels: f1}

	return &Sequence{Frames: []*Frame{frame0, frame1}}
}

// TestToGIFRemapsPerFrameTransparency proves the animated GIF exporter keeps a
// later frame's background transparent even when that frame resolves a
// different transparent index than frame 0. Before the fix, frame 1's corner
// (raw index 0) was blitted unchanged into a global palette where index 0 is
// opaque, rendering the background solid.
func TestToGIFRemapsPerFrameTransparency(t *testing.T) {
	seq := divergentSequence()

	g, err := seq.ToGIF(FallbackPalette())
	if err != nil {
		t.Fatalf("ToGIF: %v", err)
	}
	if len(g.Image) != 2 {
		t.Fatalf("got %d frames, want 2", len(g.Image))
	}

	// The global transparent index resolves to 9 (from frame 0), and that
	// palette slot must be transparent.
	if _, _, _, a := g.Image[1].Palette[9].RGBA(); a != 0 {
		t.Fatalf("global palette[9] alpha=%d, want 0 (transparent)", a)
	}

	// Frame 1's corner is on-disk index 0; it must be remapped to the global
	// transparent index 9 so it exports transparent rather than opaque.
	if got := g.Image[1].Pix[0]; got != 9 {
		t.Errorf("frame 1 corner: got palette index %d, want 9 (remapped to transparent)", got)
	}

	// Frame 1's body pixel must be untouched.
	if got := g.Image[1].Pix[5]; got != 200 {
		t.Errorf("frame 1 body pixel: got %d, want 200 (unchanged)", got)
	}
}

// TestCompositeFrameRemapsPerFrameTransparency exercises the shared compositor
// used by both the GIF and APNG exporters, proving the per-frame remap that the
// APNG path also depends on.
func TestCompositeFrameRemapsPerFrameTransparency(t *testing.T) {
	seq := divergentSequence()
	frame1 := seq.Frames[1]

	pal := FallbackPalette().ColorModel()
	frameImg := frame1.ToImageWith(FallbackPalette(), RenderOptions{Mode: TransparencyModeAuto})

	canvas := image.NewPaletted(image.Rect(0, 0, 4, 4), pal)
	for i := range canvas.Pix {
		canvas.Pix[i] = 9 // global transparent index
	}

	compositeFrameOntoCanvas(canvas, frameImg, frame1, 0, 0, 9, true,
		RenderOptions{Mode: TransparencyModeAuto})

	if got := canvas.Pix[0]; got != 9 {
		t.Errorf("composited corner: got %d, want 9 (remapped)", got)
	}
	if got := canvas.Pix[5]; got != 200 {
		t.Errorf("composited body pixel: got %d, want 200 (unchanged)", got)
	}
}

// TestToAPNGWithDivergentFramesEncodes ensures the APNG path produces a valid
// stream for a sequence with divergent per-frame transparency.
func TestToAPNGWithDivergentFramesEncodes(t *testing.T) {
	seq := divergentSequence()

	var buf bytes.Buffer
	if err := seq.ToAPNG(FallbackPalette(), &buf); err != nil {
		t.Fatalf("ToAPNG: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("ToAPNG produced no output")
	}
	// PNG signature.
	sig := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	if !bytes.HasPrefix(buf.Bytes(), sig) {
		t.Error("APNG output missing PNG signature")
	}
}
