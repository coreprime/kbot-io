package tnt

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGetFeaturePlacementsSkipsAllSentinels verifies the TA feature path
// skips every sentinel word, not just a hard-coded list. A cell holding
// 0xFFFB must not emit a placement with a huge feature index.
func TestGetFeaturePlacementsSkipsAllSentinels(t *testing.T) {
	m := makeTestMap(4, 4) // all cells start at Feature 0xFFFF (none)
	m.Header.TileAnims = 6

	// One real feature placement.
	m.TileAttr[0].Feature = 5
	// Sentinel words place nothing.
	m.TileAttr[1].Feature = 0xFFFB
	m.TileAttr[2].Feature = 0xFFFC
	m.TileAttr[3].Feature = 0xFFFE

	placements := m.GetFeaturePlacements()
	if len(placements) != 1 {
		t.Fatalf("got %d placements, want 1", len(placements))
	}
	if placements[0].FeatureIdx != 5 {
		t.Errorf("FeatureIdx: got %d, want 5", placements[0].FeatureIdx)
	}
	if placements[0].AttrX != 0 || placements[0].AttrY != 0 {
		t.Errorf("placement position: got (%d,%d), want (0,0)", placements[0].AttrX, placements[0].AttrY)
	}
}

// featureWordsMap has one placement of each of features 0 and 2, a word (5)
// beyond its 3-entry table, and the sentinels 0xFFFB, 0xFFFC and 0xFFFD.
func featureWordsMap() *Map {
	m := makeTestMap(4, 2)
	m.Header.TileAnims = 3
	for i, f := range []uint16{0, 2, 5, 0xFFFB, 0xFFFC, 0xFFFD, 0xFFFE, 0xFFFF} {
		m.TileAttr[i].Feature = f
	}
	return m
}

func TestFeaturePlacementsUseTableCount(t *testing.T) {
	m := featureWordsMap()
	got := m.GetFeaturePlacements()
	if len(got) != 2 || got[0].FeatureIdx != 0 || got[1].FeatureIdx != 2 {
		t.Fatalf("placements = %+v, want features 0 and 2 only", got)
	}
	counts := m.FeatureCounts()
	if len(counts) != 2 || counts[0] != 1 || counts[2] != 1 {
		t.Fatalf("FeatureCounts = %v, want {0:1 2:1}", counts)
	}

	// A map built in code with no header count places nothing until the
	// count is known; the explicit forms take it directly.
	m.Header.TileAnims = 0
	if got := m.GetFeaturePlacements(); len(got) != 0 {
		t.Fatalf("count 0: %d placements, want none", len(got))
	}
	if got := m.FeaturePlacementsFor(6); len(got) != 3 {
		t.Fatalf("FeaturePlacementsFor(6) = %+v, want 3 placements", got)
	}
	if got := m.FeaturePlacementsFor(-1); len(got) != 3 {
		t.Fatalf("FeaturePlacementsFor(-1) = %+v, want 3 placements below the sentinel floor", got)
	}
	if got := m.FeatureCountsFor(6); got[5] != 1 || len(got) != 3 {
		t.Fatalf("FeatureCountsFor(6) = %v", got)
	}
}

func TestPlacesFeature(t *testing.T) {
	cases := []struct {
		word  uint16
		count int
		want  bool
	}{
		{0, 1, true},
		{0, 0, false},
		{4, 4, false},
		{0xFFFA, 0x10000, true},
		{0xFFFB, 0x10000, false},
		{0xFFFF, 0x10000, false},
	}
	for _, c := range cases {
		if got := PlacesFeature(c.word, c.count); got != c.want {
			t.Errorf("PlacesFeature(%#x, %d) = %v, want %v", c.word, c.count, got, c.want)
		}
	}
}

func TestRenderBuildMapFeatureCount(t *testing.T) {
	m := featureWordsMap()
	img := m.RenderBuildMap(0)
	for x, want := range []bool{true, true, false, false} {
		if got := img.RGBAAt(x, 0) == buildMapFeatureBlock; got != want {
			t.Errorf("header count 3, cell %d feature-blocked=%v, want %v", x, got, want)
		}
	}
	if img.RGBAAt(0, 1) != buildMapVoid {
		t.Error("0xFFFC cell not void")
	}

	// No header count: fall back to the sentinel rule, so word 5 blocks
	// and the 0xFFFB..0xFFFF words do not.
	m.Header.TileAnims = 0
	img = m.RenderBuildMap(0)
	for x, want := range []bool{true, true, true, false} {
		if got := img.RGBAAt(x, 0) == buildMapFeatureBlock; got != want {
			t.Errorf("no count, cell %d feature-blocked=%v, want %v", x, got, want)
		}
	}
	for x := 1; x < 4; x++ {
		if img.RGBAAt(x, 1) == buildMapFeatureBlock {
			t.Errorf("sentinel cell (%d,1) feature-blocked", x)
		}
	}

	// An explicit count overrides both.
	img = m.RenderBuildMapFor(0, 1)
	for x, want := range []bool{true, false, false, false} {
		if got := img.RGBAAt(x, 0) == buildMapFeatureBlock; got != want {
			t.Errorf("count 1, cell %d feature-blocked=%v, want %v", x, got, want)
		}
	}
}

// TestUnpackPackKeepsDanglingFeatureWords round-trips a map whose attribute
// grid holds a feature word beyond its table. The word must stay out of
// features.csv (it places nothing) and come back from metadata.json.
func TestUnpackPackKeepsDanglingFeatureWords(t *testing.T) {
	m, feats := smallMap()
	m.TileAttr[6].Feature = 7 // beyond the 1-entry table
	m.TileAttr[7].Feature = FeatureVoid
	m.Header.TileAnims = uint32(len(feats))
	dir := t.TempDir()
	if err := Unpack(m, feats, greyPalette(), dir); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	csv, err := os.ReadFile(filepath.Join(dir, "features.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(csv, []byte("\n7,")) {
		t.Fatalf("features.csv lists the dangling word:\n%s", csv)
	}
	got, gotFeats, err := Pack(dir)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if got.TileAttr[6].Feature != 7 || got.TileAttr[7].Feature != FeatureVoid || got.TileAttr[5].Feature != 0 {
		t.Fatalf("features after Pack: %#x %#x %#x", got.TileAttr[5].Feature, got.TileAttr[6].Feature, got.TileAttr[7].Feature)
	}
	if int(got.Header.TileAnims) != len(gotFeats) || int(got.Header.Tiles) != len(got.Tiles) {
		t.Fatalf("Pack header counts tiles=%d features=%d, want %d and %d",
			got.Header.Tiles, got.Header.TileAnims, len(got.Tiles), len(gotFeats))
	}
	if img := got.RenderBuildMap(0); img.RGBAAt(1, 1) != buildMapFeatureBlock {
		t.Fatal("packed map's build map shows no feature block")
	}
}
