package gaf

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"

	"github.com/coreprime/kbot-io/palettes"
)

func taPalette(t *testing.T) *Palette {
	t.Helper()
	p, err := LoadPaletteFromBytes(palettes.DefaultPalette)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func alphaAt(img image.Image, x, y int) uint32 {
	_, _, _, a := img.At(x, y).RGBA()
	return a
}

// Palette index 0 is black to the game; only the frame's key is transparent,
// in every export format.
func TestExportsDrawIndexZeroOpaque(t *testing.T) {
	pal := taPalette(t)
	for i, c := range pal.Colors {
		if c.A != 255 {
			t.Fatalf("loaded palette entry %d has alpha %d", i, c.A)
		}
	}
	if c := FallbackPalette().Colors[0]; c != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("FallbackPalette()[0] = %v, want opaque black", c)
	}

	frame := &Frame{Width: 3, Height: 1, TransparencyIndex: 9, Pixels: []byte{0, 9, 5}}
	check := func(name string, img image.Image) {
		t.Helper()
		if a := alphaAt(img, 0, 0); a != 0xFFFF {
			t.Errorf("%s: index 0 alpha=%d, want opaque", name, a)
		}
		if r, g, b, _ := img.At(0, 0).RGBA(); r|g|b != 0 {
			t.Errorf("%s: index 0 is not black", name)
		}
		if a := alphaAt(img, 1, 0); a != 0 {
			t.Errorf("%s: key pixel alpha=%d, want transparent", name, a)
		}
		if a := alphaAt(img, 2, 0); a != 0xFFFF {
			t.Errorf("%s: pixel 5 alpha=%d, want opaque", name, a)
		}
	}

	check("ToImage", frame.ToImage(pal))

	var pngBuf bytes.Buffer
	if err := frame.ToPNG(pal, &pngBuf); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&pngBuf)
	if err != nil {
		t.Fatal(err)
	}
	check("PNG", img)

	seq := &Sequence{Frames: []*Frame{frame, frame}}
	var gifBuf bytes.Buffer
	if err := seq.WriteGIF(&gifBuf, pal); err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(&gifBuf)
	if err != nil {
		t.Fatal(err)
	}
	check("GIF", g.Image[0])

	// A caller palette whose index 0 carries alpha 0 still renders opaque.
	custom := *pal
	custom.Colors[0].A = 0
	check("caller palette", frame.ToImage(&custom))
}

// TransparencyModeNone keeps every pixel opaque, black included.
func TestNoneModeKeepsEveryPixelOpaque(t *testing.T) {
	frame := &Frame{Width: 3, Height: 1, TransparencyIndex: 9, Pixels: []byte{0, 9, 5}}
	none := RenderOptions{Mode: TransparencyModeNone}
	img := frame.ToImageWith(taPalette(t), none)
	for x := 0; x < 3; x++ {
		if a := alphaAt(img, x, 0); a != 0xFFFF {
			t.Errorf("pixel %d alpha=%d, want opaque", x, a)
		}
	}
	var buf bytes.Buffer
	if err := (&Sequence{Frames: []*Frame{frame, frame}}).WriteGIFWith(&buf, taPalette(t), none); err != nil {
		t.Fatal(err)
	}
	g, err := gif.DecodeAll(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 3; x++ {
		if a := alphaAt(g.Image[0], x, 0); a != 0xFFFF {
			t.Errorf("GIF pixel %d alpha=%d, want opaque", x, a)
		}
	}
}

// Key-valued pixels a compressed frame draws export opaque in their own
// colour; the skipped pixel takes another transparent slot.
func TestExportsKeepOpaqueKeyPixels(t *testing.T) {
	pal := taPalette(t)
	frame := &Frame{Width: 3, Height: 1, TransparencyIndex: 9, Storage: StorageCompressed,
		Pixels: []byte{9, 9, 9}, Opaque: []bool{true, false, true}}

	img := frame.ToImage(pal)
	if img.Pix[0] != 9 || img.Pix[2] != 9 || img.Pix[1] == 9 {
		t.Fatalf("Pix = %v, want [9 slot 9] with a slot other than 9", img.Pix)
	}
	for x, want := range []uint32{0xFFFF, 0, 0xFFFF} {
		if a := alphaAt(img, x, 0); a != want {
			t.Errorf("ToImage pixel %d alpha=%d, want %d", x, a, want)
		}
	}
	if img.At(0, 0) != (color.Color)(color.RGBA(pal.Colors[9])) {
		t.Errorf("pixel 0 colour %v, want palette colour 9 %v", img.At(0, 0), pal.Colors[9])
	}

	var buf bytes.Buffer
	if err := frame.ToPNG(pal, &buf); err != nil {
		t.Fatal(err)
	}
	dec, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for x, want := range []uint32{0xFFFF, 0, 0xFFFF} {
		if a := alphaAt(dec, x, 0); a != want {
			t.Errorf("PNG pixel %d alpha=%d, want %d", x, a, want)
		}
	}
}

// Composite frames export their flattened layers with the layers' coverage.
func TestExportCompositeCoverage(t *testing.T) {
	b := compositeGAF(FrameInfo{Width: 3, Height: 1, TransparencyIndex: 9, Compressed: 1},
		[]FrameInfo{{Width: 1, Height: 1}, {Width: 3, Height: 1, TransparencyIndex: 9, Compressed: 1}},
		[][]byte{{7}, {3, 0, 0x00, 9, 0x05}})
	seqs, _ := readGAF(t, b)
	img := seqs[0].Frames[0].ToImage(taPalette(t))
	// Layer 1 draws 9 at x=0 and skips the rest; layer 0 draws 7 at x=0.
	if img.Pix[0] != 9 || alphaAt(img, 0, 0) != 0xFFFF {
		t.Errorf("x=0: index %d alpha %d, want opaque 9", img.Pix[0], alphaAt(img, 0, 0))
	}
	for x := 1; x < 3; x++ {
		if a := alphaAt(img, x, 0); a != 0 {
			t.Errorf("x=%d alpha=%d, want transparent", x, a)
		}
	}
}

// PNG encoders build the whole stream before writing, so a failure leaves
// the writer untouched.
func TestPNGWritesNothingOnError(t *testing.T) {
	var buf bytes.Buffer
	empty := &Frame{Width: 0, Height: 0}
	if err := empty.ToPNG(nil, &buf); err == nil {
		t.Fatal("expected an error for a 0x0 frame")
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes written despite the error", buf.Len())
	}
	if err := (&Sequence{Frames: []*Frame{nil, {Width: 1, Height: 1, Pixels: []byte{1}}}}).ToAPNG(nil, &buf); err == nil {
		t.Fatal("expected an error for a nil frame")
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes written despite the error", buf.Len())
	}
}
