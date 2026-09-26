package tnt

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// Header word offsets used to patch synthetic files.
const (
	offWidth      = 0x04
	offHeight     = 0x08
	offMapData    = 0x0c
	offMapAttr    = 0x10
	offTileGfx    = 0x14
	offTiles      = 0x18
	offTileAnims  = 0x1c
	offTileAnim   = 0x20
	offMinimap    = 0x28
	offMinimapFlg = 0x2c
)

// smallMap returns a valid 4×4-cell (2×2-tile) map with two tiles, one
// feature placement and a 2×2 minimap.
func smallMap() (*Map, []Feature) {
	m := &Map{
		Header:   Header{IDVersion: VersionTA, SeaLevel: 10},
		TileW:    2,
		TileH:    2,
		AttrW:    4,
		AttrH:    4,
		TileMap:  []uint16{0, 1, 1, 0},
		TileAttr: make([]TileAttr, 16),
		Tiles:    [][]byte{solidTile(1), solidTile(2)},
		Minimap:  []byte{1, 2, 3, 4},
		MinimapW: 2,
		MinimapH: 2,
	}
	for i := range m.TileAttr {
		m.TileAttr[i] = TileAttr{Height: uint8(i), Feature: FeatureNone}
	}
	m.TileAttr[5].Feature = 0
	return m, []Feature{{Index: 0, Name: "rock1"}}
}

// smallTNT encodes smallMap.
func smallTNT(t *testing.T) []byte {
	t.Helper()
	m, feats := smallMap()
	var buf bytes.Buffer
	if err := m.Save(&buf, feats); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return buf.Bytes()
}

func putWord(b []byte, off int, v uint32) {
	binary.LittleEndian.PutUint32(b[off:], v)
}

func word(b []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(b[off:])
}

func TestLoadRejectsOversizedHeaderFields(t *testing.T) {
	cases := []struct {
		name  string
		patch func(b []byte)
		want  string
	}{
		{"huge dimensions", func(b []byte) {
			putWord(b, offWidth, 0xFFFFFFFF)
			putWord(b, offHeight, 0xFFFFFFFF)
		}, "attribute block"},
		{"huge tile count", func(b []byte) { putWord(b, offTiles, 0xFFFFFFFF) }, "tile graphics"},
		{"huge feature count", func(b []byte) { putWord(b, offTileAnims, 0xFFFFFFFF) }, "feature table"},
		{"attribute pointer near the end", func(b []byte) { putWord(b, offMapAttr, 0xFFFFFFF0) }, "attribute block"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := smallTNT(t)
			c.patch(b)
			_, err := LoadFromReader(bytes.NewReader(b))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming the %s", err, c.want)
			}
		})
	}
}

func TestLoadRejectsTruncatedAttributes(t *testing.T) {
	b := smallTNT(t)
	// Cut the file two bytes into the attribute block.
	cut := int(word(b, offMapAttr)) + 2
	if _, err := LoadFromReader(bytes.NewReader(b[:cut])); err == nil {
		t.Fatal("truncated attribute block loaded without error")
	}
}

func TestLoadRejectsTruncatedFeatureTable(t *testing.T) {
	b := smallTNT(t)
	cut := int(word(b, offTileAnim)) + TileAnimEntrySize - 1
	if _, err := LoadFromReader(bytes.NewReader(b[:cut])); err == nil {
		t.Fatal("truncated feature table loaded without error")
	}
}

func TestLoadIgnoresPointerOfEmptySection(t *testing.T) {
	b := smallTNT(t)
	putWord(b, offTileAnims, 0)
	putWord(b, offTileAnim, 0xFFFFFFFF)
	m, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("empty feature table with a stray pointer: %v", err)
	}
	feats, err := m.LoadFeatures(bytes.NewReader(b))
	if err != nil || feats != nil {
		t.Fatalf("LoadFeatures = %v, %v; want nil, nil", feats, err)
	}
}

func TestLoadFeaturesRejectsTableBeyondFile(t *testing.T) {
	b := smallTNT(t)
	m, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	m.Header.TileAnims = 0xFFFFFFFF
	if _, err := m.LoadFeatures(bytes.NewReader(b)); err == nil {
		t.Fatal("LoadFeatures accepted a table larger than the file")
	}
}

// TestLoadPaddingExcludesOtherSections builds a file whose tile graphics sit
// between the tile map and the attribute block. Those bytes are a section,
// not padding, so they must not be captured into MapDataPad (and written a
// second time by Save).
func TestLoadPaddingExcludesOtherSections(t *testing.T) {
	m, feats := smallMap()
	var buf bytes.Buffer
	if err := m.Save(&buf, feats); err != nil {
		t.Fatal(err)
	}
	std := buf.Bytes()
	tileMapAt := word(std, offMapData)
	attrAt := word(std, offMapAttr)
	gfxAt := word(std, offTileGfx)
	gfxLen := uint32(len(m.Tiles) * TileGfxSize)

	// Relayout: header, tile map, tile graphics, attributes, rest.
	var out []byte
	out = append(out, std[:HeaderSize]...)
	out = append(out, std[tileMapAt:attrAt]...)
	newGfx := uint32(len(out))
	out = append(out, std[gfxAt:gfxAt+gfxLen]...)
	newAttr := uint32(len(out))
	out = append(out, std[attrAt:gfxAt]...)
	shift := uint32(len(out)) - (gfxAt + gfxLen)
	out = append(out, std[gfxAt+gfxLen:]...)
	putWord(out, offMapAttr, newAttr)
	putWord(out, offTileGfx, newGfx)
	putWord(out, offTileAnim, word(std, offTileAnim)+shift)
	putWord(out, offMinimap, word(std, offMinimap)+shift)

	got, err := LoadFromReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("load relaid file: %v", err)
	}
	if len(got.MapDataPad) != 0 {
		t.Fatalf("MapDataPad captured %d bytes of the tile graphics section", len(got.MapDataPad))
	}
	if !bytes.Equal(got.Tiles[1], m.Tiles[1]) || got.TileAttr[5].Feature != 0 {
		t.Fatal("relaid sections decoded wrongly")
	}
}

func TestLoadKeepsTrailingPadding(t *testing.T) {
	m, feats := smallMap()
	m.MapDataPad = []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22}
	var buf bytes.Buffer
	if err := m.Save(&buf, feats); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.MapDataPad, m.MapDataPad) {
		t.Fatalf("MapDataPad = %x, want %x", got.MapDataPad, m.MapDataPad)
	}
	var again bytes.Buffer
	if err := got.Save(&again, feats); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.Bytes(), buf.Bytes()) {
		t.Fatal("padded map did not round-trip byte for byte")
	}
}
