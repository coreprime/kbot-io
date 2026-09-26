package pal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/color"
	"testing"

	"github.com/coreprime/kbot-io/formats/pcx"
	"github.com/coreprime/kbot-io/palettes"
)

// testPCX builds a 1x1 PCX whose colour map entry i is (i, 255-i, 7).
func testPCX(t *testing.T, version byte, marker byte) []byte {
	t.Helper()
	h := pcx.Header{Manufacturer: 0x0A, Version: version, Encoding: 1, BitsPerPixel: 8, NumPlanes: 1, BytesPerLine: 1}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, h); err != nil {
		t.Fatal(err)
	}
	buf.WriteByte(0) // the pixel
	buf.WriteByte(marker)
	for i := 0; i < 256; i++ {
		buf.Write([]byte{byte(i), byte(255 - i), 7})
	}
	return buf.Bytes()
}

type files map[string][]byte

var errNotFound = errors.New("not found")

func (f files) read(path string) ([]byte, error) {
	if data, ok := f[path]; ok {
		return data, nil
	}
	return nil, errNotFound
}

func TestLoadNamedPrefersPAL(t *testing.T) {
	long := append(append([]byte(nil), palettes.DefaultPalette...), 9, 9, 9, 9)
	fs := files{"palettes/palette.pal": long, "palettes/palette.pcx": testPCX(t, 5, pcx.PaletteMarker)}
	p, src, err := LoadNamed(fs.read, "palette")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := LoadFromBytes(palettes.DefaultPalette)
	if src != SourcePAL || !p.Equals(want) {
		t.Errorf("source %v, equal=%v; want the .pal's first 1024 bytes", src, p.Equals(want))
	}
}

func TestLoadNamedFallsBackToPCX(t *testing.T) {
	for name, fs := range map[string]files{
		"missing":   {"palettes/guipal.pcx": testPCX(t, 5, pcx.PaletteMarker)},
		"empty":     {"palettes/guipal.pal": {}, "palettes/guipal.pcx": testPCX(t, 5, pcx.PaletteMarker)},
		"no marker": {"palettes/guipal.pcx": testPCX(t, 5, 0x00)},
	} {
		p, src, err := LoadNamed(fs.read, "guipal")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if src != SourcePCX {
			t.Errorf("%s: source %v, want pcx", name, src)
		}
		if p.Colors[10] != (color.RGBA{10, 245, 7, 255}) || len(p.Raw) != FileSize || p.Raw[10*4+3] != 0 {
			t.Errorf("%s: colour 10 = %v", name, p.Colors[10])
		}
	}
}

func TestLoadNamedShortPALDoesNotFallBack(t *testing.T) {
	fs := files{"palettes/palette.pal": make([]byte, 500), "palettes/palette.pcx": testPCX(t, 5, pcx.PaletteMarker)}
	if _, _, err := LoadNamed(fs.read, "palette"); !errors.Is(err, ErrShort) {
		t.Errorf("err = %v, want ErrShort", err)
	}
}

func TestLoadNamedFailures(t *testing.T) {
	if _, _, err := LoadNamed(files{}.read, "palette"); !errors.Is(err, errNotFound) {
		t.Errorf("both missing: err = %v", err)
	}
	fs := files{"palettes/palette.pal": {}, "palettes/palette.pcx": testPCX(t, 3, pcx.PaletteMarker)}
	_, _, err := LoadNamed(fs.read, "palette")
	if !errors.Is(err, pcx.ErrGameRejects) || !errors.Is(err, ErrEmpty) {
		t.Errorf("version-3 PCX after an empty .pal: err = %v", err)
	}
}

func TestFromPCXRejectsShortFile(t *testing.T) {
	short := testPCX(t, 5, pcx.PaletteMarker)[:600]
	if _, err := FromPCX(short); !errors.Is(err, pcx.ErrGameRejects) {
		t.Errorf("err = %v, want ErrGameRejects", err)
	}
}
