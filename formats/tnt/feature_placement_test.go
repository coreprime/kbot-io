package tnt

import "testing"

// TestGetFeaturePlacementsSkipsAllSentinels verifies the TA feature path skips
// every 0xFFxx sentinel via the shared threshold, not just the three
// hard-coded values. A cell holding another 0xFFxx marker (e.g. 0xFFFB) must
// not emit a bogus placement with a huge feature index.
func TestGetFeaturePlacementsSkipsAllSentinels(t *testing.T) {
	m := makeTestMap(4, 4) // all cells start at Feature 0xFFFF (none)

	// One real feature placement.
	m.TileAttr[0].Feature = 5
	// A non-hard-coded 0xFFxx sentinel that the old list would have emitted.
	m.TileAttr[1].Feature = 0xFFFB
	// Previously-recognized sentinels stay skipped.
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
