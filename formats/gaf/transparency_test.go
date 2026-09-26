package gaf

import "testing"

// makeFrame builds a Frame backed by a flat pixel slice for transparency tests.
func makeFrame(w, h int, ti uint8, pixels []byte) *Frame {
	return &Frame{
		Width:             uint16(w),
		Height:            uint16(h),
		TransparencyIndex: ti,
		Pixels:            pixels,
	}
}

func TestEffectiveTransparencyMetadataWins(t *testing.T) {
	// 4x4 frame: every pixel is 0, TI=0. Metadata's TI is present in the
	// data — trust it.
	pixels := make([]byte, 16)
	f := makeFrame(4, 4, 0, pixels)
	if got := f.EffectiveTransparencyIndex(); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestEffectiveTransparencyTAKCornerOverride(t *testing.T) {
	// Mirrors the Tarlode case: metadata TI is 9 but pixel 9 never appears
	// in the data; all four corners are 5; the unit body uses higher values.
	// The heuristic should rescue the frame by returning 5.
	w, h := 4, 4
	pixels := make([]byte, w*h)
	for i := range pixels {
		pixels[i] = 0xE8 // unit body
	}
	pixels[0] = 5       // TL
	pixels[w-1] = 5     // TR
	pixels[(h-1)*w] = 5 // BL
	pixels[h*w-1] = 5   // BR
	f := makeFrame(w, h, 9, pixels)
	if got := f.EffectiveTransparencyIndex(); got != 5 {
		t.Errorf("got %d, want 5 (corner override)", got)
	}
}

func TestEffectiveTransparencyCornersDisagreeFallsBack(t *testing.T) {
	// dungwormB1-style: TI=9 absent from data, but the four corners don't
	// agree. The heuristic must fall back to metadata (no false override).
	pixels := []byte{
		0xE8, 0xEA, 0xEC, 0xEC,
		0xEB, 0xE9, 0xE7, 0xE8,
		0xE9, 0xEC, 0xEE, 0xEF,
		0xEF, 0xED, 0xEC, 0xEA,
	}
	f := makeFrame(4, 4, 9, pixels)
	if got := f.EffectiveTransparencyIndex(); got != 9 {
		t.Errorf("got %d, want 9 (metadata fallback)", got)
	}
}

func TestEffectiveTransparencyHandlesCorruptFrame(t *testing.T) {
	// Pixel buffer shorter than declared w*h shouldn't index OOR; just
	// return metadata TI.
	f := &Frame{Width: 100, Height: 100, TransparencyIndex: 7, Pixels: []byte{1, 2, 3}}
	if got := f.EffectiveTransparencyIndex(); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestEffectiveTransparencyZeroSize(t *testing.T) {
	// Zero-size frame: just return metadata TI without scanning.
	f := &Frame{Width: 0, Height: 0, TransparencyIndex: 42, Pixels: nil}
	if got := f.EffectiveTransparencyIndex(); got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

// The nil guard must not dereference the receiver.
func TestEffectiveTransparencyNilFrame(t *testing.T) {
	var f *Frame
	if got := f.EffectiveTransparencyIndex(); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// The heuristic only applies to raw frames: a compressed frame's skips say
// exactly what is transparent.
func TestEffectiveTransparencyIgnoresCompressedFrames(t *testing.T) {
	pixels := []byte{5, 7, 5, 7, 7, 7, 5, 7, 5}
	f := makeFrame(3, 3, 9, pixels)
	if got := f.EffectiveTransparencyIndex(); got != 5 {
		t.Fatalf("raw frame: got %d, want 5 (corner colour)", got)
	}
	f.Storage = StorageCompressed
	if got := f.EffectiveTransparencyIndex(); got != 9 {
		t.Errorf("compressed frame: got %d, want the stored key 9", got)
	}
	if rule := f.keyRule(RenderOptions{Mode: TransparencyModeHeuristic}); rule.index != 9 || !rule.useOpaque {
		t.Errorf("heuristic mode on a compressed frame: %+v, want the game's rule", rule)
	}
}

// The zero RenderOptions and the TA variant apply the stored key, never the
// corner guess; only TA: Kingdoms defaults to the heuristic.
func TestDefaultTransparencyIsTheStoredKey(t *testing.T) {
	// A fully opaque frame with a uniform border of 5; key 9 is absent.
	pixels := []byte{5, 5, 5, 5, 7, 5, 5, 5, 5}
	f := makeFrame(3, 3, 9, pixels)

	for name, opts := range map[string]RenderOptions{
		"zero":     {},
		"auto":     {Mode: TransparencyModeAuto},
		"metadata": {Mode: TransparencyModeMetadata},
		"ta":       VariantTA.DefaultRenderOptions(),
		"unknown":  VariantUnknown.DefaultRenderOptions(),
	} {
		img := f.ToImageWith(FallbackPalette(), opts)
		for i, px := range img.Pix {
			if _, _, _, a := img.Palette[px].RGBA(); a != 0xFFFF {
				t.Errorf("%s: pixel %d (index %d) is transparent; the border must stay", name, i, px)
			}
		}
	}

	if got := VariantTAK.DefaultRenderOptions(); got.Mode != TransparencyModeHeuristic {
		t.Errorf("VariantTAK default mode = %v, want TransparencyModeHeuristic", got.Mode)
	}
	img := f.ToImageWith(FallbackPalette(), VariantTAK.DefaultRenderOptions())
	if _, _, _, a := img.Palette[img.Pix[0]].RGBA(); a != 0 {
		t.Error("TA: Kingdoms heuristic should make the uniform border transparent")
	}
}
