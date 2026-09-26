package tnt

import (
	"bytes"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

func TestLoadReadsMinimapOnlyWhenFlagged(t *testing.T) {
	b := smallTNT(t)
	if word(b, offMinimapFlg)&MinimapPresent == 0 {
		t.Fatal("Save did not flag the minimap it wrote")
	}
	m, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if m.MinimapW != 2 || m.MinimapH != 2 || m.Header.MinimapFlags() != MinimapPresent {
		t.Fatalf("flagged minimap: %dx%d flags %#x", m.MinimapW, m.MinimapH, m.Header.MinimapFlags())
	}

	putWord(b, offMinimapFlg, 0)
	m, err = LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if m.Minimap != nil || m.MinimapW != 0 {
		t.Fatalf("unflagged minimap was read: %dx%d", m.MinimapW, m.MinimapH)
	}
}

func TestLoadDropsBadFlaggedMinimap(t *testing.T) {
	b := smallTNT(t)
	// Flagged, but the pointer is 0: the "minimap header" is the version
	// word and width, far over 1024, so the minimap is dropped.
	putWord(b, offMinimap, 0)
	m, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("a bad minimap must not fail the map: %v", err)
	}
	if m.Minimap != nil {
		t.Fatal("bad minimap was kept")
	}

	// Pixels running one byte past the end of the file.
	b = smallTNT(t)
	m, err = LoadFromReader(bytes.NewReader(b[:len(b)-1]))
	if err != nil {
		t.Fatalf("truncated minimap must not fail the map: %v", err)
	}
	if m.Minimap != nil {
		t.Fatal("truncated minimap was kept")
	}
}

func TestSaveSetsAndClearsMinimapFlag(t *testing.T) {
	m, feats := smallMap()
	m.Header.Unknown1 = 0x100 // an unrelated bit that must survive
	var buf bytes.Buffer
	if err := m.Save(&buf, feats); err != nil {
		t.Fatal(err)
	}
	if got := word(buf.Bytes(), offMinimapFlg); got != 0x101 {
		t.Fatalf("0x2c with a minimap = %#x, want 0x101", got)
	}

	m.Minimap, m.MinimapW, m.MinimapH = nil, 0, 0
	m.Header.Unknown1 = 0x101
	buf.Reset()
	if err := m.Save(&buf, feats); err != nil {
		t.Fatal(err)
	}
	if got := word(buf.Bytes(), offMinimapFlg); got != 0x100 {
		t.Fatalf("0x2c without a minimap = %#x, want 0x100", got)
	}
	got, err := LoadFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil || got.Minimap != nil {
		t.Fatalf("reload: minimap %v err %v", got.Minimap, err)
	}
}

func TestSaveRejectsOversizedMinimap(t *testing.T) {
	m, feats := smallMap()
	m.MinimapW, m.MinimapH = 1100, 2
	m.Minimap = make([]byte, 1100*2)
	if err := m.Save(&bytes.Buffer{}, feats); err == nil {
		t.Fatal("Save wrote a 1100-pixel-wide minimap")
	}
}

func TestMinimapContentSize(t *testing.T) {
	cases := []struct{ tilesW, tilesH, w, h int }{
		{225, 196, 252, 216},
		{128, 96, 252, 182},
		{96, 128, 193, 252},
		{64, 64, 252, 240},
		{1, 1, 252, 252}, // no visible area: full pixel size
	}
	for _, c := range cases {
		w, h := MinimapContentSize(c.tilesW*2, c.tilesH*2)
		if w != c.w || h != c.h {
			t.Errorf("%dx%d tiles: %dx%d, want %dx%d", c.tilesW, c.tilesH, w, h, c.w, c.h)
		}
	}
	if w, h := MinimapContentSize(0, 10); w != 0 || h != 0 {
		t.Errorf("empty map: %dx%d", w, h)
	}
}

func TestMinimapContentBoundsUsesDimensions(t *testing.T) {
	m := makeTestMap(256, 192) // 128×96 tiles
	m.MinimapW, m.MinimapH = MinimapSize, 256
	m.Minimap = make([]byte, MinimapSize*256)
	// Content that uses the padding colour at its edges and fills the whole
	// minimap: a pixel scan would get this wrong.
	for i := range m.Minimap {
		m.Minimap[i] = MinimapVoidByte
	}
	m.Minimap[0] = 1
	if w, h := m.MinimapContentBounds(); w != 252 || h != 182 {
		t.Fatalf("bounds %dx%d, want 252x182", w, h)
	}

	// A smaller third-party minimap keeps the map's aspect at its own size.
	m.MinimapW, m.MinimapH = 128, 128
	m.Minimap = make([]byte, 128*128)
	if w, h := m.MinimapContentBounds(); w != 128 || h != 92 {
		t.Fatalf("128x128 bounds %dx%d, want 128x92", w, h)
	}
}

// greyPalette is a 256-entry palette in which index i is grey level i.
func greyPalette() color.Palette {
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(i), uint8(i), 255}
	}
	return pal
}

func TestBuildMinimap(t *testing.T) {
	// 20×20 tiles of palette 5, whose colour is also the padding index's
	// colour: the content must still avoid the padding index.
	m := makeTestMap(40, 40)
	m.Tiles = [][]byte{solidTile(5)}
	m.TileMap = make([]uint16, m.TileW*m.TileH)
	pal := greyPalette()
	pal[5] = color.RGBA{100, 0, 0, 255}
	pal[MinimapVoidByte] = color.RGBA{100, 0, 0, 255}
	if err := m.BuildMinimap(pal); err != nil {
		t.Fatal(err)
	}
	if m.MinimapW != MinimapSize || m.MinimapH != MinimapSize || len(m.Minimap) != MinimapSize*MinimapSize {
		t.Fatalf("minimap %dx%d (%d bytes)", m.MinimapW, m.MinimapH, len(m.Minimap))
	}
	cw, ch := MinimapContentSize(m.AttrW, m.AttrH)
	if cw != 252 || ch != 212 {
		t.Fatalf("content size %dx%d, want 252x212", cw, ch)
	}
	for y := 0; y < MinimapSize; y++ {
		for x := 0; x < MinimapSize; x++ {
			v := m.Minimap[y*MinimapSize+x]
			if x < cw && y < ch {
				if v != 5 {
					t.Fatalf("content pixel (%d,%d) = %#x, want 5", x, y, v)
				}
			} else if v != MinimapVoidByte {
				t.Fatalf("padding pixel (%d,%d) = %#x", x, y, v)
			}
		}
	}
	if w, h := m.MinimapContentBounds(); w != cw || h != ch {
		t.Errorf("MinimapContentBounds = %dx%d, want %dx%d", w, h, cw, ch)
	}

	var buf bytes.Buffer
	if err := m.Save(&buf, nil); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil || !bytes.Equal(got.Minimap, m.Minimap) {
		t.Fatalf("built minimap did not survive Save/Load: %v", err)
	}
}

func TestBuildMinimapAveragesTiles(t *testing.T) {
	// 18×12 tiles (visible 544×256 px): the left half of the map is palette
	// 50, the right half palette 150, so the minimap is 50 on the left and
	// 150 on the right, with at most a blended column between.
	m := makeTestMap(36, 24)
	m.Tiles = [][]byte{solidTile(50), solidTile(150)}
	m.TileMap = make([]uint16, m.TileW*m.TileH)
	for ty := 0; ty < m.TileH; ty++ {
		for tx := 9; tx < m.TileW; tx++ {
			m.TileMap[ty*m.TileW+tx] = 1
		}
	}
	if err := m.BuildMinimap(greyPalette()); err != nil {
		t.Fatal(err)
	}
	cw, ch := MinimapContentSize(m.AttrW, m.AttrH)
	if m.Minimap[0] != 50 || m.Minimap[cw-1] != 150 || m.Minimap[(ch-1)*MinimapSize] != 50 {
		t.Fatalf("corners %d %d %d, want 50 150 50", m.Minimap[0], m.Minimap[cw-1], m.Minimap[(ch-1)*MinimapSize])
	}
	blended := 0
	for x := 0; x < cw; x++ {
		if v := m.Minimap[x]; v != 50 && v != 150 {
			blended++
			if v < 50 || v > 150 {
				t.Fatalf("pixel %d = %d, outside the two tile colours", x, v)
			}
		}
	}
	if blended > 1 {
		t.Fatalf("%d blended columns, want at most 1", blended)
	}
}

func TestBuildMinimapRejectsBadInput(t *testing.T) {
	m := makeTestMap(4, 4)
	if err := m.BuildMinimap(nil); err == nil {
		t.Error("empty palette accepted")
	}
	if err := m.BuildMinimap(greyPalette()); err == nil {
		t.Error("map without a tile map accepted")
	}
}

// TestRetailMinimapsFlagged checks every shipped map flags its minimap and
// that the content bounds come out as the game's region.
func TestRetailMinimapsFlagged(t *testing.T) {
	root := testutil.UnpackedDir(t, "maps")
	files, _ := filepath.Glob(filepath.Join(root, "*.tnt"))
	if len(files) == 0 {
		t.Skip("no maps")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m, err := LoadFromReader(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if m.Header.MinimapFlags()&MinimapPresent == 0 || m.Minimap == nil {
			t.Errorf("%s: minimap not flagged or not read", filepath.Base(f))
			continue
		}
		w, h := m.MinimapContentBounds()
		cw, ch := MinimapContentSize(m.AttrW, m.AttrH)
		if w != cw || h != ch {
			t.Errorf("%s: bounds %dx%d, want %dx%d", filepath.Base(f), w, h, cw, ch)
		}
	}
}
