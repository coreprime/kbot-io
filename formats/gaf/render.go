package gaf

import (
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
)

// maxCanvasPixels caps the canvas of an animated export. Frame origins are
// signed 16-bit values, so frames placed far apart could otherwise ask for a
// canvas of billions of pixels.
const maxCanvasPixels = maxFramePixels

// exportPalette is the palette shared by the images of one export: the
// caller's colours made opaque, with at most one entry (slot) transparent.
type exportPalette struct {
	rgb     color.Palette // opaque colours, for PNG PLTE chunks
	colors  color.Palette // rgb with colors[slot] transparent when hasSlot
	slot    uint8
	hasSlot bool
}

// newExportPalette picks the transparent slot for frames rendered under
// rules. The slot is a palette index that no opaque pixel of any frame uses,
// preferring a frame's own key, so that opaque key-valued pixels of
// compressed frames and frames with different keys all render correctly.
// background asks for a slot even when no rule applies transparency (for
// canvas area no frame covers). When every index is used by opaque pixels,
// the first applied key becomes the slot and pixels of that value render
// transparent.
func newExportPalette(palette *Palette, frames []*Frame, rules []keyRule, background bool) exportPalette {
	if palette == nil {
		palette = FallbackPalette()
	}
	ep := exportPalette{rgb: make(color.Palette, len(palette.Colors))}
	for i, c := range palette.Colors {
		c.A = 255
		ep.rgb[i] = c
	}
	ep.colors = ep.rgb

	need := background
	for _, r := range rules {
		need = need || r.apply
	}
	if need {
		var used [256]bool
		for fi, f := range frames {
			n := min(int(f.Width)*int(f.Height), len(f.Pixels))
			for i := 0; i < n; i++ {
				if !f.transparentAt(i, rules[fi]) {
					used[f.Pixels[i]] = true
				}
			}
		}
		ep.slot, ep.hasSlot = pickSlot(&used, rules)
	}
	if ep.hasSlot {
		ep.colors = make(color.Palette, len(ep.rgb))
		copy(ep.colors, ep.rgb)
		ep.colors[ep.slot] = color.Transparent
	}
	return ep
}

func pickSlot(used *[256]bool, rules []keyRule) (uint8, bool) {
	for _, r := range rules {
		if r.apply && !used[r.index] {
			return r.index, true
		}
	}
	for i := range used {
		if !used[i] {
			return uint8(i), true
		}
	}
	for _, r := range rules {
		if r.apply {
			return r.index, true
		}
	}
	return 0, false
}

// background returns the index for pixels nothing draws.
func (ep exportPalette) background(fallback uint8) uint8 {
	if ep.hasSlot {
		return ep.slot
	}
	return fallback
}

// drawFrame copies the pixels of f that render opaque under rule onto canvas,
// with f's top-left at (ox, oy).
func drawFrame(canvas *image.Paletted, f *Frame, rule keyRule, ox, oy int) {
	cw, ch := canvas.Rect.Dx(), canvas.Rect.Dy()
	fw, fh := int(f.Width), int(f.Height)
	for y := 0; y < fh; y++ {
		ty := oy + y
		if ty < 0 || ty >= ch {
			continue
		}
		for x := 0; x < fw; x++ {
			tx := ox + x
			if tx < 0 || tx >= cw {
				continue
			}
			i := y*fw + x
			if f.transparentAt(i, rule) {
				continue
			}
			canvas.Pix[ty*canvas.Stride+tx] = f.Pixels[i]
		}
	}
}

// ToImage converts a frame to an image.Image using the given palette and the
// game's transparency rule (see TransparencyModeMetadata).
func (f *Frame) ToImage(palette *Palette) *image.Paletted {
	return f.ToImageWith(palette, RenderOptions{})
}

// ToImageWith renders a frame with explicit transparency handling.
//
// Every palette entry is opaque except one transparent slot. Pixels keep
// their palette index, except transparent ones, which take the slot: normally
// the frame's key, or another unused index when the frame draws key-valued
// pixels (see Frame.Opaque). A nil palette uses FallbackPalette. A pixel
// buffer shorter than Width*Height is padded with transparent pixels.
func (f *Frame) ToImageWith(palette *Palette, opts RenderOptions) *image.Paletted {
	img, _ := f.render(palette, opts)
	return img
}

func (f *Frame) render(palette *Palette, opts RenderOptions) (*image.Paletted, exportPalette) {
	rule := f.keyRule(opts)
	size := int(f.Width) * int(f.Height)
	ep := newExportPalette(palette, []*Frame{f}, []keyRule{rule}, len(f.Pixels) < size)
	img := image.NewPaletted(image.Rect(0, 0, int(f.Width), int(f.Height)), ep.colors)
	bg := ep.background(f.TransparencyIndex)
	for i := range img.Pix {
		if f.transparentAt(i, rule) {
			img.Pix[i] = bg
		} else {
			img.Pix[i] = f.Pixels[i]
		}
	}
	return img, ep
}

// sequenceCanvases is a sequence rendered onto one shared canvas size.
type sequenceCanvases struct {
	images        []*image.Paletted
	palette       exportPalette
	width, height int
}

// renderCanvases places every frame of s on a canvas that holds all of them
// aligned by hotspot. Frames without pixels give blank canvases.
func (s *Sequence) renderCanvases(palette *Palette, opts RenderOptions) (*sequenceCanvases, error) {
	if len(s.Frames) == 0 {
		return nil, fmt.Errorf("no frames in sequence")
	}
	// Frame extends from (-OriginX, -OriginY) to (Width-OriginX,
	// Height-OriginY) relative to the hotspot.
	var minX, minY, maxX, maxY int
	found := false
	for i, f := range s.Frames {
		if f == nil {
			return nil, fmt.Errorf("frame %d is nil", i)
		}
		if f.Width == 0 || f.Height == 0 {
			continue
		}
		left, top := -int(f.OriginX), -int(f.OriginY)
		right, bottom := left+int(f.Width), top+int(f.Height)
		if !found || left < minX {
			minX = left
		}
		if !found || top < minY {
			minY = top
		}
		if !found || right > maxX {
			maxX = right
		}
		if !found || bottom > maxY {
			maxY = bottom
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("no valid frames with non-zero dimensions")
	}
	cw, ch := maxX-minX, maxY-minY
	if int64(cw)*int64(ch) > maxCanvasPixels {
		return nil, fmt.Errorf("animation canvas %dx%d exceeds maximum of %d pixels", cw, ch, maxCanvasPixels)
	}

	rules := make([]keyRule, len(s.Frames))
	background := false
	for i, f := range s.Frames {
		rules[i] = f.keyRule(opts)
		if int(f.Width) != cw || int(f.Height) != ch || len(f.Pixels) < int(f.Width)*int(f.Height) {
			background = true
		}
	}
	ep := newExportPalette(palette, s.Frames, rules, background)
	bg := ep.background(0)

	out := &sequenceCanvases{palette: ep, width: cw, height: ch, images: make([]*image.Paletted, 0, len(s.Frames))}
	for i, f := range s.Frames {
		canvas := image.NewPaletted(image.Rect(0, 0, cw, ch), ep.colors)
		for p := range canvas.Pix {
			canvas.Pix[p] = bg
		}
		drawFrame(canvas, f, rules[i], -int(f.OriginX)-minX, -int(f.OriginY)-minY)
		out.images = append(out.images, canvas)
	}
	return out, nil
}

// ToGIF converts a sequence to an animated GIF using the game's transparency
// rule.
func (s *Sequence) ToGIF(palette *Palette) (*gif.GIF, error) {
	return s.ToGIFWith(palette, RenderOptions{})
}

// ToGIFWith converts a sequence to an animated GIF using explicit transparency
// options.
//
// Frames are aligned by hotspot on a canvas that holds all of them. The
// transparent slot is chosen across the whole sequence, so every frame's
// transparent pixels (whatever its own key) and the canvas area a frame does
// not cover render transparent, while palette index 0 stays opaque black.
//
// Timing follows the game: each frame shows for DisplayTicks ticks of 1/30 s.
// GIF delays are whole hundredths of a second, so they are rounded on the
// running total and the animation keeps the game's overall speed. The GIF
// loops forever when the sequence loops (Sequence.Loops) and plays once
// otherwise. Each frame replaces the previous one (background disposal), so
// transparent areas never show earlier frames.
func (s *Sequence) ToGIFWith(palette *Palette, opts RenderOptions) (*gif.GIF, error) {
	sc, err := s.renderCanvases(palette, opts)
	if err != nil {
		return nil, err
	}
	g := &gif.GIF{
		Image:    sc.images,
		Delay:    make([]int, 0, len(s.Frames)),
		Disposal: make([]byte, 0, len(s.Frames)),
		Config: image.Config{
			Width:      sc.width,
			Height:     sc.height,
			ColorModel: sc.palette.colors,
		},
	}
	if !s.Loops() {
		g.LoopCount = -1
	}
	elapsed, shown := 0, 0 // ticks, hundredths of a second
	for _, frame := range s.Frames {
		elapsed += frame.DisplayTicks()
		due := (elapsed*100 + TicksPerSecond/2) / TicksPerSecond
		g.Delay = append(g.Delay, due-shown)
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
		shown = due
	}
	return g, nil
}

// WriteGIF writes an animated GIF to the given writer using the game's
// transparency rule.
func (s *Sequence) WriteGIF(w io.Writer, palette *Palette) error {
	return s.WriteGIFWith(w, palette, RenderOptions{})
}

// WriteGIFWith writes an animated GIF using explicit transparency options.
func (s *Sequence) WriteGIFWith(w io.Writer, palette *Palette, opts RenderOptions) error {
	g, err := s.ToGIFWith(palette, opts)
	if err != nil {
		return err
	}
	return gif.EncodeAll(w, g)
}
