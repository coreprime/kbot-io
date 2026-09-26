package tnt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveRejectsInvalidMaps(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(m *Map, feats *[]Feature)
		wantE string
	}{
		{"tile grid not half the attribute grid", func(m *Map, _ *[]Feature) {
			m.AttrW, m.TileW = 4, 1
			m.TileMap = []uint16{0, 0}
		}, "not half"},
		{"too many tiles", func(m *Map, _ *[]Feature) {
			tile := solidTile(0)
			m.Tiles = make([][]byte, MaxTiles+1)
			for i := range m.Tiles {
				m.Tiles[i] = tile
			}
		}, "at most 65536"},
		{"feature table reaching the sentinel floor", func(_ *Map, feats *[]Feature) {
			*feats = make([]Feature, FeatureSentinelFloor)
			for i := range *feats {
				(*feats)[i].Name = "rock"
			}
		}, "fewer than"},
		{"feature name without room for a terminator", func(_ *Map, feats *[]Feature) {
			(*feats)[0].Name = strings.Repeat("x", 128)
		}, "shorter than 128"},
		{"tile index beyond the tile set", func(m *Map, _ *[]Feature) {
			m.TileMap[3] = 2
		}, "beyond the 2-tile set"},
		{"feature word beyond the table", func(_ *Map, feats *[]Feature) {
			*feats = nil // forgot LoadFeatures: cell 5 still names feature 0
		}, "beyond the 0-entry feature table"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, feats := smallMap()
			c.edit(m, &feats)
			err := m.Save(&bytes.Buffer{}, feats)
			if err == nil || !strings.Contains(err.Error(), c.wantE) {
				t.Fatalf("Save = %v, want an error containing %q", err, c.wantE)
			}
		})
	}
}

func TestSaveAcceptsLongestFeatureName(t *testing.T) {
	m, feats := smallMap()
	feats[0].Name = strings.Repeat("x", 127)
	var buf bytes.Buffer
	if err := m.Save(&buf, feats); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := got.LoadFeatures(bytes.NewReader(buf.Bytes()))
	if err != nil || loaded[0].Name != feats[0].Name {
		t.Fatalf("name did not round-trip: %v", err)
	}
}

func TestSaveAllowUnresolvedIndices(t *testing.T) {
	m, feats := smallMap()
	m.TileMap[3] = 9
	m.TileAttr[6].Feature = 4
	var buf bytes.Buffer
	if err := m.SaveWithOptions(&buf, feats, SaveOptions{AllowUnresolvedIndices: true}); err != nil {
		t.Fatalf("SaveWithOptions: %v", err)
	}
	got, err := LoadFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.TileMap[3] != 9 || got.TileAttr[6].Feature != 4 {
		t.Fatalf("indices not kept: tile %d feature %d", got.TileMap[3], got.TileAttr[6].Feature)
	}
	if n := len(got.GetFeaturePlacements()); n != 1 {
		t.Fatalf("%d placements, want 1 (the dangling word places nothing)", n)
	}
}

func TestSaveWarnings(t *testing.T) {
	collect := func(m *Map, feats []Feature) []string {
		t.Helper()
		var msgs []string
		if err := m.SaveWithOptions(&bytes.Buffer{}, feats, SaveOptions{Warn: func(s string) { msgs = append(msgs, s) }}); err != nil {
			t.Fatal(err)
		}
		return msgs
	}

	m, feats := smallMap() // 2×2 minimap
	if msgs := collect(m, feats); len(msgs) != 1 || !strings.Contains(msgs[0], "at least 252") {
		t.Fatalf("small minimap warnings = %q", msgs)
	}

	m.Minimap = make([]byte, MinimapSize*MinimapSize)
	m.MinimapW, m.MinimapH = MinimapSize, MinimapSize
	if msgs := collect(m, feats); len(msgs) != 0 {
		t.Fatalf("252x252 minimap warnings = %q", msgs)
	}

	m.Minimap, m.MinimapW, m.MinimapH = nil, 0, 0
	if msgs := collect(m, feats); len(msgs) != 1 || !strings.Contains(msgs[0], "no minimap") {
		t.Fatalf("no-minimap warnings = %q", msgs)
	}

	// A 4098×2-cell strip exceeds the 4096-cell side bound.
	wide := makeTestMap(4098, 2)
	wide.Tiles = [][]byte{solidTile(0)}
	wide.TileMap = make([]uint16, wide.TileW*wide.TileH)
	wide.Minimap = make([]byte, MinimapSize*MinimapSize)
	wide.MinimapW, wide.MinimapH = MinimapSize, MinimapSize
	if msgs := collect(wide, nil); len(msgs) != 1 || !strings.Contains(msgs[0], "4096") {
		t.Fatalf("wide map warnings = %q", msgs)
	}

	if msgs := m.interchangeWarnings(InterchangeMaxFileSize + 1); !strings.Contains(strings.Join(msgs, "\n"), "bytes") {
		t.Fatalf("oversized file warnings = %q", msgs)
	}
}

// unpackSmall unpacks smallMap into a fresh directory.
func unpackSmall(t *testing.T) string {
	t.Helper()
	m, feats := smallMap()
	dir := t.TempDir()
	if err := Unpack(m, feats, greyPalette(), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPackRejectsBadTileIndices(t *testing.T) {
	for _, bad := range []string{"-1", "70000", "2"} {
		t.Run(bad, func(t *testing.T) {
			dir := unpackSmall(t)
			csv := "0," + bad + "\n1,0\n"
			if err := os.WriteFile(filepath.Join(dir, "tilemap.csv"), []byte(csv), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Pack(dir); err == nil {
				t.Fatalf("Pack accepted tile index %s", bad)
			}
		})
	}

	dir := unpackSmall(t)
	if err := os.WriteFile(filepath.Join(dir, "tilemap.csv"), []byte("0,5\n1,0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _, err := PackWithOptions(dir, PackOptions{AllowUnresolvedIndices: true})
	if err != nil {
		t.Fatalf("PackWithOptions: %v", err)
	}
	if m.TileMap[1] != 5 {
		t.Fatalf("tile index %d, want 5", m.TileMap[1])
	}
}

func TestPackRejectsFeatureIndexOutsideTable(t *testing.T) {
	dir := unpackSmall(t)
	csv := "feature_index,name,attr_x,attr_y\n65532,rock1,1,1\n"
	if err := os.WriteFile(filepath.Join(dir, "features.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Pack(dir); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("Pack = %v, want an out-of-range feature error", err)
	}
}
