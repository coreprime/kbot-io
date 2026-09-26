package gaf

// TransparencyMode selects which pixels of a frame render as transparent.
// The zero value, TransparencyModeAuto, applies the game's rule.
type TransparencyMode int

const (
	// TransparencyModeAuto applies the game's rule, the same as
	// TransparencyModeMetadata. It is the zero value, so a zero
	// RenderOptions and the convenience methods (ToImage, ToGIF, ToPNG,
	// ToAPNG) render frames as the game draws them. Earlier versions guessed
	// a key from the pixel data here; that guess is now
	// TransparencyModeHeuristic.
	TransparencyModeAuto TransparencyMode = iota
	// TransparencyModeMetadata applies the game's rule: a raw frame's pixels
	// equal to its stored TransparencyIndex are transparent, and a
	// compressed frame's skipped pixels are (see Frame.PixelOpaque).
	// Palette index 0 is opaque black like any other colour.
	TransparencyModeMetadata
	// TransparencyModeNone disables transparency for the render — every
	// pixel of the frame is opaque.
	TransparencyModeNone
	// TransparencyModeIndex makes every pixel equal to RenderOptions.Index
	// transparent, ignoring the frame's own key and coverage.
	TransparencyModeIndex
	// TransparencyModeHeuristic suits TA: Kingdoms raw texture atlases,
	// whose stored TransparencyIndex often differs from the colour the
	// artist filled the background with. For raw frames it uses
	// EffectiveTransparencyIndex, which may pick a uniform corner colour;
	// compressed and composite frames use the game's rule, since their
	// skipped pixels are authoritative. VariantTAK.DefaultRenderOptions
	// selects it. The game itself never guesses.
	TransparencyModeHeuristic
)

// RenderOptions controls per-render transparency choices. A zero-valued
// RenderOptions applies the game's rule (TransparencyModeAuto).
type RenderOptions struct {
	Mode  TransparencyMode
	Index uint8 // honored when Mode == TransparencyModeIndex
}

// keyRule is the resolved transparency rule for one frame and render.
type keyRule struct {
	apply     bool  // any pixel may be transparent
	index     uint8 // transparent value when coverage is not used
	useOpaque bool  // honour Frame.Opaque (the game's rule)
}

// keyRule resolves opts for this frame.
func (f *Frame) keyRule(opts RenderOptions) keyRule {
	switch opts.Mode {
	case TransparencyModeNone:
		return keyRule{}
	case TransparencyModeIndex:
		return keyRule{apply: true, index: opts.Index}
	case TransparencyModeHeuristic:
		if f.Storage != StorageCompressed && f.Opaque == nil && len(f.Layers) == 0 {
			return keyRule{apply: true, index: f.EffectiveTransparencyIndex()}
		}
	}
	return keyRule{apply: true, index: f.TransparencyIndex, useOpaque: true}
}

// transparentAt reports whether pixel i renders transparent under rule. An
// index outside Pixels (a short, corrupt buffer) is transparent.
func (f *Frame) transparentAt(i int, rule keyRule) bool {
	if i >= len(f.Pixels) {
		return true
	}
	if !rule.apply {
		return false
	}
	if rule.useOpaque {
		return !f.PixelOpaque(i)
	}
	return f.Pixels[i] == rule.index
}

// EffectiveTransparencyIndex returns the key TransparencyModeHeuristic uses
// for this frame. It never changes TransparencyIndex, so writers still see
// the stored byte.
//
// For raw frames built by TA: Kingdoms artists the stored key often differs
// from the pixel value used as the transparent fill (for example the key is
// 9 but the background pixels are 5). The heuristic:
//
//  1. If TransparencyIndex appears anywhere in the pixel data, trust it.
//  2. Otherwise, if the four corner pixels agree, use their value (the
//     artist's uniform border).
//  3. Otherwise fall back to TransparencyIndex.
//
// Compressed and composite frames, whose skipped pixels say exactly what is
// transparent, and frames with no pixels always return TransparencyIndex. A
// nil frame returns 0.
//
// The game never does this: it always uses the stored key for raw frames. A
// fully opaque TA frame with a uniform border would lose that border under
// the heuristic, so TA renders should use TransparencyModeMetadata (the
// default).
func (f *Frame) EffectiveTransparencyIndex() uint8 {
	if f == nil {
		return 0
	}
	if f.Storage == StorageCompressed || len(f.Layers) > 0 || len(f.Pixels) == 0 || f.Width == 0 || f.Height == 0 {
		return f.TransparencyIndex
	}
	// Cheap scan: if metadata TI is present in the pixel data, prefer it.
	for _, p := range f.Pixels {
		if p == f.TransparencyIndex {
			return f.TransparencyIndex
		}
	}
	w := int(f.Width)
	h := int(f.Height)
	if w*h != len(f.Pixels) {
		// Mismatched pixel buffer (corrupt frame) — fall back to metadata
		// rather than indexing out of range.
		return f.TransparencyIndex
	}
	tl := f.Pixels[0]
	tr := f.Pixels[w-1]
	bl := f.Pixels[(h-1)*w]
	br := f.Pixels[h*w-1]
	if tl == tr && tr == bl && bl == br {
		return tl
	}
	return f.TransparencyIndex
}
