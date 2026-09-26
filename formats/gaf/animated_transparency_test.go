package gaf

import (
	"bytes"
	"image/png"
	"testing"
)

// divergentSequence builds a 2-frame sequence whose frames resolve DIFFERENT
// transparent indices under TransparencyModeHeuristic, mirroring TA: Kingdoms
// uncompressed atlases:
//
//   - Frame 0: TransparencyIndex 9 is present in the pixel data, so it resolves
//     to 9 — this becomes the animation-wide transparent slot.
//   - Frame 1: TransparencyIndex 9 is absent from the data but all four corners
//     are 0, so the corner heuristic resolves it to 0. Its transparent
//     background (index 0) differs from the slot (9).
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

var heuristic = RenderOptions{Mode: TransparencyModeHeuristic}

// TestToGIFRemapsPerFrameTransparency proves the animated GIF exporter keeps a
// later frame's background transparent even when that frame resolves a
// different transparent index than frame 0.
func TestToGIFRemapsPerFrameTransparency(t *testing.T) {
	seq := divergentSequence()

	g, err := seq.ToGIFWith(FallbackPalette(), heuristic)
	if err != nil {
		t.Fatalf("ToGIFWith: %v", err)
	}
	if len(g.Image) != 2 {
		t.Fatalf("got %d frames, want 2", len(g.Image))
	}

	// The transparent slot is frame 0's key, 9, and only it is transparent.
	for i, c := range g.Image[1].Palette {
		_, _, _, a := c.RGBA()
		if (i == 9) != (a == 0) {
			t.Fatalf("palette[%d] alpha=%d; only slot 9 should be transparent", i, a)
		}
	}

	// Frame 1's corner is on-disk index 0; it must take the slot so it
	// exports transparent rather than opaque.
	if got := g.Image[1].Pix[0]; got != 9 {
		t.Errorf("frame 1 corner: got palette index %d, want 9 (remapped to transparent)", got)
	}

	// Frame 1's body pixel must be untouched.
	if got := g.Image[1].Pix[5]; got != 200 {
		t.Errorf("frame 1 body pixel: got %d, want 200 (unchanged)", got)
	}
}

// Under the default (game) rule the same frames keep black: frame 1's zero
// pixels are opaque black, not a guessed background.
func TestToGIFDefaultKeepsBlackOpaque(t *testing.T) {
	g, err := divergentSequence().ToGIF(FallbackPalette())
	if err != nil {
		t.Fatalf("ToGIF: %v", err)
	}
	if got := g.Image[1].Pix[0]; got != 0 {
		t.Fatalf("frame 1 corner: got index %d, want 0", got)
	}
	if _, _, _, a := g.Image[1].Palette[0].RGBA(); a != 0xFFFF {
		t.Errorf("palette[0] alpha=%d, want opaque", a)
	}
	if _, _, _, a := g.Image[0].Palette[9].RGBA(); a != 0 {
		t.Errorf("palette[9] (frame 0's key) alpha=%d, want transparent", a)
	}
}

// When a later frame uses the first frame's key as an opaque colour, the
// exporter picks another slot so that colour stays visible.
func TestSequenceSlotAvoidsOpaqueColours(t *testing.T) {
	a := &Frame{Width: 2, Height: 1, TransparencyIndex: 9, Pixels: []byte{9, 4}}
	b := &Frame{Width: 2, Height: 1, TransparencyIndex: 0, Pixels: []byte{9, 0}}
	g, err := (&Sequence{Frames: []*Frame{a, b}}).ToGIF(FallbackPalette())
	if err != nil {
		t.Fatal(err)
	}
	slot := g.Image[0].Pix[0]
	if slot == 9 || slot == 4 {
		t.Fatalf("slot %d collides with an opaque colour", slot)
	}
	if g.Image[1].Pix[0] != 9 || g.Image[1].Pix[1] != slot {
		t.Errorf("frame 1 = %v, want [9 %d]", g.Image[1].Pix, slot)
	}
	if _, _, _, alpha := g.Image[1].Palette[9].RGBA(); alpha == 0 {
		t.Error("opaque colour 9 rendered transparent")
	}
}

// TestToAPNGWithDivergentFramesEncodes ensures the APNG path produces a valid
// stream for a sequence with divergent per-frame transparency.
func TestToAPNGWithDivergentFramesEncodes(t *testing.T) {
	seq := divergentSequence()

	var buf bytes.Buffer
	if err := seq.ToAPNGWith(FallbackPalette(), heuristic, &buf); err != nil {
		t.Fatalf("ToAPNGWith: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("ToAPNGWith produced no output")
	}
	if !bytes.HasPrefix(buf.Bytes(), pngSignature) {
		t.Error("APNG output missing PNG signature")
	}
	// The default image decoder reads the first frame.
	if _, err := png.Decode(bytes.NewReader(buf.Bytes())); err != nil {
		t.Errorf("APNG does not decode as PNG: %v", err)
	}
}
