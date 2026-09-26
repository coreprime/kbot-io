package pcx

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

func TestGameDecodeFullWidthImage(t *testing.T) {
	r := load(t, fullWidthPCX(t))
	img, err := r.DecodeGame()
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 65536 || b.Dy() != 1 {
		t.Fatalf("bounds %v, want 65536x1", b)
	}
	if img.Pix[0] != 7 || img.Pix[65535] != 7 || r.Truncated() {
		t.Fatalf("first/last pixel %d/%d truncated=%v", img.Pix[0], img.Pix[65535], r.Truncated())
	}
}

func TestGameIgnoresBytesPerLine(t *testing.T) {
	// A 3x2 image padded to BytesPerLine 4; the pad byte is 9.
	data := buildPCX(t, pcxSpec{width: 3, height: 2, bpl: 4, pixels: []byte{1, 2, 3, 9, 4, 5, 6, 9}})
	game, err := load(t, data).DecodeGame()
	if err != nil {
		t.Fatal(err)
	}
	// The game reads width bytes per row, so the pad byte starts row 1.
	if got := pixels(t, game); !bytes.Equal(got, []byte{1, 2, 3, 9, 4, 5}) {
		t.Errorf("game rows = %v, want [1 2 3 9 4 5]", got)
	}
	rep := load(t, data).Compat()
	if !rep.Has(CompatBytesPerLine) || !rep.GameLoads() || len(rep.Issues) != 1 {
		t.Errorf("expected a bytes-per-line warning only, got %v", rep.Issues)
	}
}

func TestGameRunClippingAndZeroLengthRuns(t *testing.T) {
	data := buildPCX(t, pcxSpec{width: 3, height: 2, pixels: []byte{0xC0, 0x55, 0xC5, 7, 1, 2, 3}})
	img, err := load(t, data).DecodeGame()
	if err != nil {
		t.Fatal(err)
	}
	if got := pixels(t, img); !bytes.Equal(got, []byte{7, 7, 7, 1, 2, 3}) {
		t.Errorf("rows = %v, want [7 7 7 1 2 3]", got)
	}
}

func TestGamePaletteIgnoresMarker(t *testing.T) {
	data := buildPCX(t, pcxSpec{width: 2, height: 1, pixels: []byte{0, 1}, noMarker: true})
	r := load(t, data)
	if r.HasEmbeddedPalette() {
		t.Fatal("file without a marker reported an embedded palette")
	}
	m := testColorMap()
	want := color.RGBA{m[3], m[4], m[5], 255}
	gp := r.GamePalette()
	if gp == nil || gp[1].(color.RGBA) != want {
		t.Fatalf("GamePalette[1] = %v, want %v", gp, want)
	}
	game, err := r.DecodeGame()
	if err != nil {
		t.Fatal(err)
	}
	if c := game.Palette[1].(color.RGBA); c != want {
		t.Errorf("game palette[1] = %v, want the file's colour map", c)
	}
	if rep := r.Compat(); !rep.Has(CompatPaletteMarker) || !rep.GameLoads() {
		t.Errorf("expected a palette-marker warning, got %v", rep.Issues)
	}
}

func TestGamePalettesAreOpaque(t *testing.T) {
	for _, noMarker := range []bool{false, true} {
		data := buildPCX(t, pcxSpec{width: 1, height: 1, pixels: []byte{0}, noMarker: noMarker})
		img, err := load(t, data).DecodeGame()
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range img.Palette {
			if _, _, _, a := c.RGBA(); a != 0xFFFF {
				t.Fatalf("noMarker=%v: palette index %d is not opaque", noMarker, i)
			}
		}
	}
}

func TestGameRejectsShortFile(t *testing.T) {
	// Header plus one pixel byte: too short for the 768-byte colour map.
	r := load(t, buildPCX(t, pcxSpec{width: 1, height: 1, pixels: []byte{1}, noColorMap: true}))
	if r.GamePalette() != nil {
		t.Error("GamePalette should be nil for a file shorter than header plus colour map")
	}
	if _, err := r.DecodeGame(); !errors.Is(err, ErrGameRejects) {
		t.Errorf("DecodeGame error = %v, want ErrGameRejects", err)
	}
	if rep := r.Compat(); rep.GameLoads() || !rep.Has(CompatShortFile) {
		t.Errorf("expected a short-file error, got %v", rep.Issues)
	}
}

func TestGameRejectsOtherVersions(t *testing.T) {
	r := load(t, buildPCX(t, pcxSpec{version: 3, width: 1, height: 1, pixels: []byte{4}}))
	if _, err := r.DecodeGame(); !errors.Is(err, ErrGameRejects) || !strings.Contains(err.Error(), "version 3") {
		t.Errorf("DecodeGame error = %v, want a version rejection", err)
	}
	if _, err := r.Decode(); err != nil {
		t.Errorf("standard decode should still accept version 3: %v", err)
	}
	rep := r.Compat()
	if rep.GameLoads() || !rep.Has(CompatVersion) || rep.Issues[0].Severity != CompatError {
		t.Errorf("expected a version error, got %v", rep.Issues)
	}
}

func TestGameRejectsInvertedBounds(t *testing.T) {
	r := load(t, buildPCX(t, pcxSpec{xMin: 5, xMax: 4}))
	if _, err := r.DecodeGame(); !errors.Is(err, ErrGameRejects) {
		t.Errorf("DecodeGame error = %v, want ErrGameRejects", err)
	}
	if rep := r.Compat(); !rep.Has(CompatBounds) || rep.GameLoads() {
		t.Errorf("expected a bounds error, got %v", rep.Issues)
	}
}

func TestGameDecodesAnyDepthAsEightBit(t *testing.T) {
	// A 2x1 24-bit file: three planes of 2 bytes each.
	data := buildPCX(t, pcxSpec{planes: 3, width: 2, height: 1, pixels: []byte{10, 11, 20, 21, 30, 31}})
	std, err := load(t, data).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if c := std.At(1, 0).(color.RGBA); c != (color.RGBA{11, 21, 31, 255}) {
		t.Errorf("standard 24-bit pixel = %v", c)
	}
	game, err := load(t, data).DecodeGame()
	if err != nil {
		t.Fatal(err)
	}
	if got := pixels(t, game); !bytes.Equal(got, []byte{10, 11}) {
		t.Errorf("game pixels = %v, want the first two bytes as indices", got)
	}
	if rep := load(t, data).Compat(); !rep.Has(CompatDepth) || !rep.GameLoads() {
		t.Errorf("expected a depth warning, got %v", rep.Issues)
	}
	if rep := load(t, buildPCX(t, pcxSpec{encoding: 2, width: 1, height: 1, pixels: []byte{1}})).Compat(); !rep.Has(CompatEncoding) {
		t.Errorf("expected an encoding warning, got %v", rep.Issues)
	}
}

func TestGameTruncatedPixelData(t *testing.T) {
	// 400x30 pixels cannot come out of one run plus the colour map bytes, so
	// the rows run out before the end.
	r := load(t, buildPCX(t, pcxSpec{width: 400, height: 30, pixels: []byte{0xFF, 1}}))
	img, err := r.DecodeGame()
	if err != nil || !r.Truncated() {
		t.Fatalf("game truncated decode: err=%v truncated=%v", err, r.Truncated())
	}
	if img.Pix[0] != 1 || img.Pix[len(img.Pix)-1] != 0 {
		t.Errorf("first/last pixel = %d/%d, want 1/0", img.Pix[0], img.Pix[len(img.Pix)-1])
	}
	if rep := r.Compat(); !rep.Has(CompatTruncated) || !rep.GameLoads() {
		t.Errorf("expected a truncated warning, got %v", rep.Issues)
	}
}

func TestRowsRunIntoColourMap(t *testing.T) {
	// A 2x1 image with one pixel byte: the second pixel is the 0x0C marker.
	data := buildPCX(t, pcxSpec{width: 2, height: 1, pixels: []byte{5}})
	if rep := load(t, data).Compat(); !rep.Has(CompatRowsInPalette) {
		t.Errorf("expected a rows-in-palette warning, got %v", rep.Issues)
	}
	game, err := load(t, data).DecodeGame()
	if err != nil {
		t.Fatal(err)
	}
	if got := pixels(t, game); !bytes.Equal(got, []byte{5, PaletteMarker}) {
		t.Errorf("game pixels = %v", got)
	}
}

func TestCompatCleanFile(t *testing.T) {
	rep := load(t, buildPCX(t, pcxSpec{width: 2, height: 2, pixels: []byte{0xC2, 1, 2, 3}})).Compat()
	if !rep.OK() || !rep.GameLoads() {
		t.Errorf("clean file reported %v", rep.Issues)
	}
}

func TestDecodeWithOptions(t *testing.T) {
	data := buildPCX(t, pcxSpec{width: 3, height: 2, bpl: 4, pixels: []byte{1, 2, 3, 9, 4, 5, 6, 9}})
	r := load(t, data)
	std, err := r.DecodeWithOptions(DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	game, err := r.DecodeWithOptions(DecodeOptions{Mode: ModeGame})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(pixels(t, std), pixels(t, game)) {
		t.Error("standard and game modes should differ for a padded file")
	}
	if _, err := r.DecodeWithOptions(DecodeOptions{Mode: DecodeMode(99)}); err == nil {
		t.Error("expected an error for an unknown mode")
	}
	if _, err := load(t, buildPCX(t, pcxSpec{version: 2, width: 1, height: 1})).DecodeWithOptions(DecodeOptions{Mode: ModeGame}); !errors.Is(err, ErrGameRejects) {
		t.Errorf("game mode error = %v, want ErrGameRejects", err)
	}
}

func TestConvertToPNGWithOptionsGame(t *testing.T) {
	data := buildPCX(t, pcxSpec{width: 3, height: 2, bpl: 4, pixels: []byte{1, 2, 3, 9, 4, 5, 6, 9}})
	var buf bytes.Buffer
	if err := ConvertToPNGWithOptions(&buf, bytes.NewReader(data), DecodeOptions{Mode: ModeGame}); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	m := testColorMap()
	// Row 1 starts with the pad byte 9 in game mode.
	if c := color.RGBAModel.Convert(img.At(0, 1)).(color.RGBA); c != (color.RGBA{m[27], m[28], m[29], 255}) {
		t.Errorf("pixel (0,1) = %v, want colour 9", c)
	}
}

// TestRetailPCXGameCompatible checks every retail TA PCX: the game rules
// report nothing and the game decode matches the standard decode.
func TestRetailPCXGameCompatible(t *testing.T) {
	root := testutil.UnpackedPath(t)
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".pcx") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		r, err := LoadFromReader(bytes.NewReader(data))
		if err != nil {
			return nil // palettes/guipal.pcx holds .PAL bytes under a .pcx name
		}
		count++
		if rep := r.Compat(); !rep.OK() {
			t.Errorf("%s: %v", path, rep.Issues)
		}
		std, err := r.Decode()
		if err != nil {
			t.Errorf("%s: standard decode: %v", path, err)
			return nil
		}
		game, err := r.DecodeGame()
		if err != nil {
			t.Errorf("%s: game decode: %v", path, err)
			return nil
		}
		ps := std.(*image.Paletted)
		if !bytes.Equal(ps.Pix, game.Pix) {
			t.Errorf("%s: game and standard pixels differ", path)
		}
		for i := range ps.Palette {
			if ps.Palette[i] != game.Palette[i] {
				t.Errorf("%s: palette entry %d differs", path, i)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 400 {
		t.Errorf("checked only %d PCX files", count)
	}
}
