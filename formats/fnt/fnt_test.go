package fnt

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

// wantRoman10AGlyph is the expected bitmap of capital 'A' in roman10.fnt. It
// visibly reads as an "A", which locks the MSB-first bit convention: an
// LSB-first misread would mirror every row and destroy the shape.
var wantRoman10AGlyph = []string{
	"..........",
	"....#.....",
	"....#.....",
	"...#.#....",
	"...#.#....",
	"..#...#...",
	"..#####...",
	".#.....#..",
	".#.....#..",
	"###...###.",
	"..........",
	"..........",
	"..........",
}

// roman10Hash is a golden checksum over every decoded glyph's dimensions and
// pixel data. Any change to the bit-unpacking convention shifts this value.
const roman10Hash = "64b4d58cd7eff0addc9d1fb1d153741b16bfc01015974c4586e36692cce83ce2"

func loadRoman10(t *testing.T) *Font {
	t.Helper()
	path := testutil.UnpackedFile(t, "fonts", "roman10.fnt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open roman10.fnt: %v", err)
	}
	defer func() { _ = f.Close() }()

	font, err := LoadFromReader(f)
	if err != nil {
		t.Fatalf("LoadFromReader: %v", err)
	}
	return font
}

// TestRoman10MSBConvention renders capital 'A' and checks it against the golden
// bitmap, proving glyph bits are unpacked MSB-first.
func TestRoman10MSBConvention(t *testing.T) {
	font := loadRoman10(t)

	g := font.Glyphs['A']
	if g == nil {
		t.Fatal("glyph 'A' missing")
	}
	if g.Width != 10 || g.Height != 13 {
		t.Fatalf("glyph 'A' dimensions: got %dx%d, want 10x13", g.Width, g.Height)
	}

	for y := 0; y < g.Height; y++ {
		var row strings.Builder
		for x := 0; x < g.Width; x++ {
			if g.Pixels[y*g.Width+x] {
				row.WriteByte('#')
			} else {
				row.WriteByte('.')
			}
		}
		if got := row.String(); got != wantRoman10AGlyph[y] {
			t.Errorf("glyph 'A' row %d: got %q, want %q", y, got, wantRoman10AGlyph[y])
		}
	}
}

// TestRoman10GoldenHash locks the full decoded bitmap of roman10.fnt.
func TestRoman10GoldenHash(t *testing.T) {
	font := loadRoman10(t)

	if font.Height != 13 {
		t.Errorf("font height: got %d, want 13", font.Height)
	}
	if got := font.GlyphCount(); got != 94 {
		t.Errorf("glyph count: got %d, want 94", got)
	}

	h := sha256.New()
	for ch := 0; ch < 256; ch++ {
		g := font.Glyphs[ch]
		if g == nil {
			continue
		}
		h.Write([]byte{byte(ch), byte(g.Width), byte(g.Height)})
		for _, p := range g.Pixels {
			if p {
				h.Write([]byte{1})
			} else {
				h.Write([]byte{0})
			}
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != roman10Hash {
		t.Errorf("golden hash mismatch:\n got  %s\n want %s", got, roman10Hash)
	}
}
