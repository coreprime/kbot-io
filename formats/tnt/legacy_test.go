package tnt

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// legacyTNT hand-builds a 0x1020 map: 4×2 attribute cells (2×1 tiles), one
// tile, one feature and a 2×1 minimap addressed by the words at 0x38/0x3c.
func legacyTNT() []byte {
	const (
		attrW, attrH = 4, 2
		tileMapAt    = HeaderSize
		attrAt       = tileMapAt + 2*2
		gfxAt        = attrAt + attrW*attrH*8
		featAt       = gfxAt + TileGfxSize
		minimapAt    = featAt + TileAnimEntrySize
		total        = minimapAt + 8 + 2
	)
	b := make([]byte, total)
	le := binary.LittleEndian
	for i, v := range []uint32{
		VersionLegacy, attrW, attrH, tileMapAt, attrAt, gfxAt,
		1,      // tiles
		1,      // features
		featAt, // feature table
		40,     // sea level
		0, 0, 0, 0,
		minimapAt, // 0x38 minimap pointer
		1,         // 0x3c presence flags
	} {
		le.PutUint32(b[i*4:], v)
	}
	// Tile map: both tiles use tile 0.
	// Attributes: height i*10, feature byte per cell, per-cell byte at +6.
	featureBytes := []byte{0, 0xFF, 0xFC, 0xFE, 0xFF, 0xFF, 0xFD, 0xFF}
	for i := 0; i < attrW*attrH; i++ {
		rec := b[attrAt+i*8:]
		rec[0] = byte(i * 10)
		rec[1] = 0x55
		rec[2] = featureBytes[i]
		rec[6] = byte(100 + i)
	}
	for i := 0; i < TileGfxSize; i++ {
		b[gfxAt+i] = 7
	}
	le.PutUint32(b[featAt:], 0)
	copy(b[featAt+4:], "tree1")
	le.PutUint32(b[minimapAt:], 2)
	le.PutUint32(b[minimapAt+4:], 1)
	b[minimapAt+8], b[minimapAt+9] = 9, 8
	return b
}

func TestLoadLegacyMap(t *testing.T) {
	b := legacyTNT()
	m, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("load 0x1020 map: %v", err)
	}
	if !m.IsLegacy() || m.IsTAK {
		t.Fatalf("IsLegacy=%v IsTAK=%v", m.IsLegacy(), m.IsTAK)
	}
	if m.TileW != 2 || m.TileH != 1 || len(m.TileAttr) != 8 || len(m.LegacyAttr) != 8 {
		t.Fatalf("dimensions: tiles %dx%d, %d attrs, %d legacy attrs", m.TileW, m.TileH, len(m.TileAttr), len(m.LegacyAttr))
	}
	wantFeature := []uint16{0, FeatureNone, FeatureNone, FeatureNone, FeatureNone, FeatureNone, FeatureNone, FeatureNone}
	for i, a := range m.TileAttr {
		if a.Height != byte(i*10) || a.Feature != wantFeature[i] {
			t.Errorf("cell %d: height %d feature %#x, want %d %#x", i, a.Height, a.Feature, i*10, wantFeature[i])
		}
		if m.LegacyAttr[i][6] != byte(100+i) || m.LegacyAttr[i][1] != 0x55 {
			t.Errorf("cell %d: raw legacy record %x not preserved", i, m.LegacyAttr[i])
		}
	}
	if m.MinimapW != 2 || m.MinimapH != 1 || !bytes.Equal(m.Minimap, []byte{9, 8}) {
		t.Errorf("minimap %dx%d %v, want 2x1 [9 8] from the 0x38 pointer", m.MinimapW, m.MinimapH, m.Minimap)
	}
	feats, err := m.LoadFeatures(bytes.NewReader(b))
	if err != nil || len(feats) != 1 || feats[0].Name != "tree1" {
		t.Fatalf("LoadFeatures = %+v, %v", feats, err)
	}
	if p := m.GetFeaturePlacements(); len(p) != 1 || p[0].FeatureIdx != 0 || p[0].AttrX != 0 {
		t.Errorf("placements = %+v, want one feature 0 at (0,0)", p)
	}
}

func TestSaveConvertsLegacyMap(t *testing.T) {
	b := legacyTNT()
	m, err := LoadFromReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	feats, err := m.LoadFeatures(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := m.Save(&buf, feats); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out := buf.Bytes()
	if v := word(out, 0); v != VersionTA {
		t.Fatalf("saved version %#x, want %#x", v, VersionTA)
	}
	if word(out, 0x38) != 0 || word(out, 0x3c) != 0 {
		t.Errorf("legacy minimap words not cleared: %#x %#x", word(out, 0x38), word(out, 0x3c))
	}
	got, err := LoadFromReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.LegacyAttr != nil {
		t.Error("a 0x2000 map has LegacyAttr")
	}
	for i := range m.TileAttr {
		if got.TileAttr[i].Height != m.TileAttr[i].Height || got.TileAttr[i].Feature != m.TileAttr[i].Feature {
			t.Errorf("cell %d: %+v, want %+v", i, got.TileAttr[i], m.TileAttr[i])
		}
	}
	if got.Header.SeaLevel != 40 || !bytes.Equal(got.Minimap, m.Minimap) {
		t.Errorf("sea level %d minimap %v", got.Header.SeaLevel, got.Minimap)
	}
}

func TestLoadRejectsUnknownVersion(t *testing.T) {
	b := legacyTNT()
	putWord(b, 0, 0x3000)
	if _, err := LoadFromReader(bytes.NewReader(b)); err == nil {
		t.Fatal("version 0x3000 loaded")
	}
}
