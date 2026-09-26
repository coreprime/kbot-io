package tnt

import (
	"image/color"
	"testing"
)

func grayPalette256() color.Palette {
	pal := make(color.Palette, 256)
	for i := 0; i < 256; i++ {
		pal[i] = color.RGBA{uint8(i), uint8(i), uint8(i), 255}
	}
	return pal
}

func TestLintReportsDuplicates(t *testing.T) {
	tiles := [][]byte{
		solidTile(10),
		solidTile(10), // duplicate
		solidTile(20),
	}
	tilemap := []uint16{0, 1, 2, 0}
	heights := make([]uint8, 16)
	m := buildSyntheticMap(tiles, tilemap, heights)

	diags, err := m.Lint(LintOptions{SimilarityPercent: 0})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	// Lint must not mutate the caller's map.
	if len(m.Tiles) != 3 {
		t.Fatalf("Lint mutated the map: Tiles=%d, want 3", len(m.Tiles))
	}
	if got := findDiag(diags, "duplicate-tiles"); got == nil {
		t.Fatalf("duplicate-tiles diagnostic missing; got %+v", diags)
	} else {
		if got.Count != 1 {
			t.Errorf("duplicate-tiles Count = %d, want 1", got.Count)
		}
		if got.BytesSaved != TileGfxSize {
			t.Errorf("duplicate-tiles BytesSaved = %d, want %d", got.BytesSaved, TileGfxSize)
		}
	}
}

func TestLintReportsUnused(t *testing.T) {
	tiles := [][]byte{
		solidTile(10),
		solidTile(99), // unused
	}
	tilemap := []uint16{0, 0, 0, 0}
	heights := make([]uint8, 16)
	m := buildSyntheticMap(tiles, tilemap, heights)

	diags, err := m.Lint(LintOptions{SimilarityPercent: 0})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	got := findDiag(diags, "unused-tiles")
	if got == nil || got.Count != 1 {
		t.Fatalf("unused-tiles count = %v, want 1; diags=%+v", got, diags)
	}
}

func TestLintReportsSimilarity(t *testing.T) {
	tiles := [][]byte{
		solidTile(40),
		solidTile(41), // close enough to 40 in greyscale palette
	}
	tilemap := []uint16{0, 1, 0, 1}
	heights := make([]uint8, 16) // all zero, identical footprint
	m := buildSyntheticMap(tiles, tilemap, heights)

	diags, err := m.Lint(LintOptions{SimilarityPercent: 1.0, Palette: grayPalette256()})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	got := findDiag(diags, "similar-tiles")
	if got == nil || got.Count != 1 {
		t.Fatalf("similar-tiles count = %v, want 1; diags=%+v", got, diags)
	}
	if got.BytesSaved != TileGfxSize {
		t.Errorf("similar-tiles BytesSaved = %d, want %d", got.BytesSaved, TileGfxSize)
	}
}

func TestLintCleanMap(t *testing.T) {
	tiles := [][]byte{
		solidTile(10),
		solidTile(200),
	}
	tilemap := []uint16{0, 1, 0, 1}
	heights := make([]uint8, 16)
	m := buildSyntheticMap(tiles, tilemap, heights)
	m.Minimap = make([]byte, MinimapSize*MinimapSize)
	m.MinimapW, m.MinimapH = MinimapSize, MinimapSize

	diags, err := m.Lint(LintOptions{SimilarityPercent: 1.0, Palette: grayPalette256()})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("clean map should produce no diagnostics, got %+v", diags)
	}
}

func TestLintRequiresPaletteWhenSimilarityEnabled(t *testing.T) {
	tiles := [][]byte{solidTile(10), solidTile(11)}
	tilemap := []uint16{0, 1, 0, 1}
	heights := make([]uint8, 16)
	m := buildSyntheticMap(tiles, tilemap, heights)

	if _, err := m.Lint(LintOptions{SimilarityPercent: 1.0}); err == nil {
		t.Fatalf("Lint(similarity>0, no palette) should fail")
	}
}

func findDiag(diags []LintDiagnostic, rule string) *LintDiagnostic {
	for i := range diags {
		if diags[i].Rule == rule {
			return &diags[i]
		}
	}
	return nil
}

// TestOptimizeAndLintWithBadTileIndex covers a tile map that names a tile
// beyond the tile set alongside a duplicate and an unused tile. Neither
// Optimize nor Lint may panic; the bad index stays beyond the smaller set.
func TestOptimizeAndLintWithBadTileIndex(t *testing.T) {
	tiles := [][]byte{
		solidTile(10),
		solidTile(10), // duplicate of 0
		solidTile(30), // unused
	}
	tilemap := []uint16{0, 1, 7, 0}
	m := buildSyntheticMap(tiles, tilemap, make([]uint8, 16))

	diags, err := m.Lint(LintOptions{})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	bad := findDiag(diags, LintRuleBadTileIndex)
	if bad == nil || bad.Count != 1 || bad.Severity != LintWarning {
		t.Fatalf("bad-tile-index = %+v; diags=%+v", bad, diags)
	}
	if d := findDiag(diags, LintRuleDuplicateTiles); d == nil || d.Count != 1 {
		t.Fatalf("duplicate-tiles = %+v", d)
	}
	if d := findDiag(diags, LintRuleUnusedTiles); d == nil || d.Count != 1 {
		t.Fatalf("unused-tiles = %+v", d)
	}

	stats, err := m.Optimize(OptimizeOptions{})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if stats.TilesAfter != 1 || len(m.Tiles) != 1 {
		t.Fatalf("tiles after = %d, want 1", len(m.Tiles))
	}
	if m.TileMap[0] != 0 || m.TileMap[1] != 0 || m.TileMap[3] != 0 {
		t.Fatalf("tile map %v: in-range cells should map to tile 0", m.TileMap)
	}
	if m.TileMap[2] != 7 {
		t.Fatalf("bad index rewritten to %d, want it left at 7", m.TileMap[2])
	}
}

func TestOptimizeRejectsInconsistentGrids(t *testing.T) {
	m := buildSyntheticMap([][]byte{solidTile(1)}, []uint16{0, 0, 0, 0}, make([]uint8, 16))
	m.TileAttr = m.TileAttr[:4]
	if _, err := m.Optimize(OptimizeOptions{}); err == nil {
		t.Fatal("Optimize accepted an attribute grid shorter than AttrW*AttrH")
	}
}

func TestLintValidityRules(t *testing.T) {
	m := buildSyntheticMap([][]byte{solidTile(1)}, []uint16{0, 0, 0, 0}, make([]uint8, 16))
	m.Header.TileAnims = 2
	m.TileAttr[0].Feature = 1           // placed
	m.TileAttr[1].Feature = 2           // beyond the table
	m.TileAttr[2].Feature = 9           // beyond the table
	m.TileAttr[3].Feature = FeatureVoid // sentinel, fine
	m.Minimap = make([]byte, 16)
	m.MinimapW, m.MinimapH = 4, 4

	diags, err := m.Lint(LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d := findDiag(diags, LintRuleUnresolvedFeature); d == nil || d.Count != 2 || d.Severity != LintWarning {
		t.Fatalf("unresolved-feature = %+v", d)
	}
	if d := findDiag(diags, LintRuleMinimap); d == nil || d.Severity != LintWarning {
		t.Fatalf("minimap = %+v", d)
	}
	if d := findDiag(diags, LintRuleBadTileIndex); d != nil {
		t.Fatalf("unexpected bad-tile-index %+v", d)
	}

	m.Minimap, m.MinimapW, m.MinimapH = nil, 0, 0
	diags, _ = m.Lint(LintOptions{})
	if d := findDiag(diags, LintRuleMinimap); d == nil || d.Severity != LintInfo {
		t.Fatalf("missing minimap = %+v", d)
	}

	wide := makeTestMap(4098, 2)
	wide.Tiles = [][]byte{solidTile(0)}
	wide.TileMap = make([]uint16, wide.TileW*wide.TileH)
	diags, err = wide.Lint(LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d := findDiag(diags, LintRuleInterchangeBounds); d == nil || d.Count != 1 {
		t.Fatalf("interchange-bounds = %+v", d)
	}
}
