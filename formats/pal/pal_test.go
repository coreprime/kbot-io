package pal

import (
	"bytes"
	"errors"
	"image/color"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/palettes"
	"github.com/coreprime/kbot-io/testutil"
)

func TestLoadEmbeddedPalette(t *testing.T) {
	p, err := LoadFromBytes(palettes.DefaultPalette)
	if err != nil {
		t.Fatalf("LoadFromBytes failed: %v", err)
	}
	if p.Colors[0].A != 0 {
		t.Errorf("index 0 should have alpha 0, got %d", p.Colors[0].A)
	}
	for i := 1; i < EntryCount; i++ {
		if p.Colors[i].A != 255 {
			t.Errorf("index %d should be opaque, got alpha %d", i, p.Colors[i].A)
		}
	}
	if !p.IsLikelyTAPalette() {
		t.Error("embedded palette should be flagged as TA palette")
	}
}

func TestRoundTripBytes(t *testing.T) {
	p, err := LoadFromBytes(palettes.DefaultPalette)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), palettes.DefaultPalette) {
		t.Error("round-trip changed bytes")
	}
}

func TestWriteJASC(t *testing.T) {
	p, _ := LoadFromBytes(palettes.DefaultPalette)
	var buf bytes.Buffer
	if err := p.WriteJASC(&buf); err != nil {
		t.Fatalf("jasc: %v", err)
	}
	text := buf.String()
	if !strings.HasPrefix(text, "JASC-PAL\n0100\n256\n") {
		t.Errorf("missing JASC header: %q", text[:32])
	}
	if got := strings.Count(text, "\n"); got != 256+3 {
		t.Errorf("expected 259 newlines (header + 256 rows), got %d", got)
	}
}

func TestWriteGPL(t *testing.T) {
	p, _ := LoadFromBytes(palettes.DefaultPalette)
	var buf bytes.Buffer
	if err := p.WriteGPL(&buf, "Test"); err != nil {
		t.Fatalf("gpl: %v", err)
	}
	text := buf.String()
	if !strings.Contains(text, "GIMP Palette") || !strings.Contains(text, "Name: Test") {
		t.Errorf("missing GPL preamble: %q", text[:60])
	}
}

func TestRenderSwatch(t *testing.T) {
	p, _ := LoadFromBytes(palettes.DefaultPalette)
	img := p.RenderSwatch(8)
	if img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Errorf("expected 128x128, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	// Last entry should match the palette's RGB exactly.
	gotR, gotG, gotB, _ := img.At(127, 127).RGBA()
	want := p.Colors[255]
	if uint8(gotR>>8) != want.R || uint8(gotG>>8) != want.G || uint8(gotB>>8) != want.B {
		t.Errorf("last cell mismatch: got %d/%d/%d want %d/%d/%d",
			gotR>>8, gotG>>8, gotB>>8, want.R, want.G, want.B)
	}
}

func TestRejectsWrongSize(t *testing.T) {
	if _, err := LoadFromBytes(make([]byte, 100)); !errors.Is(err, ErrShort) {
		t.Errorf("100 bytes: err = %v, want ErrShort", err)
	}
	if _, err := LoadFromBytes(nil); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: err = %v, want ErrEmpty", err)
	}
	if _, err := LoadFromReader(bytes.NewReader(nil)); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty stream: err = %v, want ErrEmpty", err)
	}
	if _, err := LoadFromReader(bytes.NewReader(make([]byte, 1023))); !errors.Is(err, ErrShort) {
		t.Errorf("1023-byte stream: err = %v, want ErrShort", err)
	}
}

func TestLongerFileUsesFirst1024Bytes(t *testing.T) {
	data := append(append([]byte(nil), palettes.DefaultPalette...), 1, 2, 3, 4, 5, 6, 7, 8)
	for name, load := range map[string]func() (*Palette, error){
		"bytes":  func() (*Palette, error) { return LoadFromBytes(data) },
		"reader": func() (*Palette, error) { return LoadFromReader(bytes.NewReader(data)) },
	} {
		p, err := load()
		if err != nil {
			t.Fatalf("%s: a 1032-byte palette should load: %v", name, err)
		}
		if !bytes.Equal(p.Raw, palettes.DefaultPalette) {
			t.Errorf("%s: Raw should be the first 1024 bytes", name)
		}
		want, _ := LoadFromBytes(palettes.DefaultPalette)
		if !p.Equals(want) {
			t.Errorf("%s: colours differ from the first 1024 bytes", name)
		}
	}
}

func TestWriteUsesColorsAndKeepsFourthByte(t *testing.T) {
	data := append([]byte(nil), palettes.DefaultPalette...)
	data[5*4+3] = 7
	p, err := LoadFromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	p.Colors[3] = color.RGBA{1, 2, 3, 255}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if len(out) != FileSize {
		t.Fatalf("wrote %d bytes, want %d", len(out), FileSize)
	}
	if !bytes.Equal(out[12:15], []byte{1, 2, 3}) {
		t.Errorf("edited entry 3 = %v, want [1 2 3]", out[12:15])
	}
	if out[5*4+3] != 7 {
		t.Errorf("fourth byte of entry 5 = %d, want 7", out[5*4+3])
	}
	fresh := &Palette{Colors: p.Colors}
	buf.Reset()
	if err := fresh.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.Bytes()[5*4+3] != 0 {
		t.Error("a palette without Raw should write zero fourth bytes")
	}
}

func TestOpaqueColorModel(t *testing.T) {
	p, _ := LoadFromBytes(palettes.DefaultPalette)
	for i, c := range p.OpaqueColorModel() {
		rgba := c.(color.RGBA)
		if rgba.A != 255 {
			t.Fatalf("index %d alpha = %d, want 255", i, rgba.A)
		}
		if rgba.R != p.Colors[i].R || rgba.G != p.Colors[i].G || rgba.B != p.Colors[i].B {
			t.Fatalf("index %d colour changed", i)
		}
	}
	if p.ColorModel()[0].(color.RGBA).A != 0 {
		t.Error("ColorModel should keep index 0 transparent")
	}
}

func TestColorModel(t *testing.T) {
	p, _ := LoadFromBytes(palettes.DefaultPalette)
	cm := p.ColorModel()
	if len(cm) != EntryCount {
		t.Errorf("ColorModel size = %d, want %d", len(cm), EntryCount)
	}
	if _, ok := cm[5].(color.RGBA); !ok {
		t.Errorf("ColorModel entries should be color.RGBA")
	}
}

func TestEquals(t *testing.T) {
	a, _ := LoadFromBytes(palettes.DefaultPalette)
	b, _ := LoadFromBytes(palettes.DefaultPalette)
	if !a.Equals(b) {
		t.Error("identical palettes should compare equal")
	}
	b.Colors[10] = color.RGBA{99, 99, 99, 255}
	if a.Equals(b) {
		t.Error("differing palettes should not compare equal")
	}
}

func TestLoadFromUnpackedPalette(t *testing.T) {
	path := testutil.UnpackedFile(t, "palettes", "palette.pal")
	p, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile %s: %v", path, err)
	}
	if len(p.Raw) != FileSize {
		t.Errorf("raw size = %d, want %d", len(p.Raw), FileSize)
	}
	unique, _ := p.Histogram()
	if unique < 100 {
		t.Errorf("expected at least 100 unique colors, got %d", unique)
	}
}

func TestLoadLookup(t *testing.T) {
	path := testutil.UnpackedFile(t, "palettes", "palette.alp")
	table, err := LoadLookupFromFile(path)
	if err != nil {
		t.Fatalf("LoadLookupFromFile: %v", err)
	}
	if len(table) != AlphaTableSize {
		t.Errorf("lookup size = %d, want %d", len(table), AlphaTableSize)
	}

	pal, _ := LoadFromBytes(palettes.DefaultPalette)
	img, err := RenderLookupSwatch(table, pal, 2)
	if err != nil {
		t.Fatalf("RenderLookupSwatch: %v", err)
	}
	if img.Bounds().Dx() != 512 || img.Bounds().Dy() != 512 {
		t.Errorf("expected 512x512, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}
