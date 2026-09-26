package tnt

import (
	"fmt"
	"image/color"
	"strings"
)

// LintSeverity classifies a TNT lint finding.  Size-reduction
// opportunities are LintInfo; problems that change how the game or other
// readers see the map are LintWarning.  The type is kept severity-shaped to
// match the COB linter and keep the explorer's lint UI uniform across
// formats.
type LintSeverity string

const (
	// LintInfo is an informational finding — typically a hint that
	// kbot tnt optimize could remove some redundancy.
	LintInfo LintSeverity = "info"
	// LintWarning is a potentially-impactful finding.
	LintWarning LintSeverity = "warning"
)

// Rule names reported in LintDiagnostic.Rule.
const (
	LintRuleDuplicateTiles    = "duplicate-tiles"
	LintRuleSimilarTiles      = "similar-tiles"
	LintRuleUnusedTiles       = "unused-tiles"
	LintRuleBadTileIndex      = "bad-tile-index"
	LintRuleUnresolvedFeature = "unresolved-feature"
	LintRuleInterchangeBounds = "interchange-bounds"
	LintRuleMinimap           = "minimap"
)

// LintDiagnostic is one finding from Map.Lint.  Count and BytesSaved
// duplicate information that Message also contains in human form so
// that the explorer can render badges, sort, and filter without parsing
// the Message string.
type LintDiagnostic struct {
	Rule       string       `json:"rule"`
	Severity   LintSeverity `json:"severity"`
	Message    string       `json:"message"`
	Count      int          `json:"count"`
	BytesSaved int          `json:"bytes_saved"`
}

// LintOptions tunes Map.Lint.
type LintOptions struct {
	// SimilarityPercent is the maximum mean per-channel pixel difference
	// (% of 255) for the similar-tiles rule.  Set to 0 to skip the
	// similarity check.  Required > 0 also implies Palette must be set.
	SimilarityPercent float64

	// Palette converts paletted tile bytes to RGB when scoring similarity.
	// Required when SimilarityPercent > 0.
	Palette color.Palette
}

// Lint inspects the map without mutating it.  The warning rules report
// data the game reads differently from what the map intends, or that other
// readers may refuse (TA maps only):
//
//	bad-tile-index      tile map cells naming a tile beyond the tile set;
//	                    the game reads past its tile set there
//	unresolved-feature  cells whose feature word is below the sentinel
//	                    floor but not below Header.TileAnims; the game
//	                    places nothing there
//	interchange-bounds  a map beyond the interchange bounds
//	                    (InterchangeMaxAttrSide, InterchangeMaxFileSize)
//	minimap             no minimap (info), or one too small for the game's
//	                    radar (warning)
//
// The info rules report size-reduction opportunities; each mirrors a pass
// of Map.Optimize:
//
//	duplicate-tiles  tile graphics with byte-identical pixel data
//	similar-tiles    visually-similar tile graphics whose placements
//	                 share the same heightmap footprint
//	unused-tiles     tile graphics that no map cell references
//
// Lint runs Optimize on a deep copy of m, so the caller's map is left
// untouched and the reported counts match exactly what `kbot tnt
// optimize` would remove.
func (m *Map) Lint(opts LintOptions) ([]LintDiagnostic, error) {
	if m == nil {
		return nil, fmt.Errorf("nil map")
	}
	cp := m.clone()
	stats, err := cp.Optimize(OptimizeOptions{
		SimilarityPercent: opts.SimilarityPercent,
		Palette:           opts.Palette,
	})
	if err != nil {
		return nil, err
	}

	diags := m.validityDiagnostics()
	if stats.ExactMerges > 0 {
		n := stats.ExactMerges
		diags = append(diags, LintDiagnostic{
			Rule:       LintRuleDuplicateTiles,
			Severity:   LintInfo,
			Count:      n,
			BytesSaved: n * TileGfxSize,
			Message: fmt.Sprintf(
				"%d byte-identical duplicate tile graphic%s — consolidating saves %d bytes",
				n, pluralS(n), n*TileGfxSize),
		})
	}
	if stats.SimilarityMerges > 0 {
		n := stats.SimilarityMerges
		diags = append(diags, LintDiagnostic{
			Rule:       LintRuleSimilarTiles,
			Severity:   LintInfo,
			Count:      n,
			BytesSaved: n * TileGfxSize,
			Message: fmt.Sprintf(
				"%d tile graphic%s within ≤%g%% visual similarity (same heightmap footprint) — consolidating saves %d bytes",
				n, pluralS(n), opts.SimilarityPercent, n*TileGfxSize),
		})
	}
	if stats.UnusedRemoved > 0 {
		n := stats.UnusedRemoved
		diags = append(diags, LintDiagnostic{
			Rule:       LintRuleUnusedTiles,
			Severity:   LintInfo,
			Count:      n,
			BytesSaved: n * TileGfxSize,
			Message: fmt.Sprintf(
				"%d unreferenced tile graphic%s — removing saves %d bytes",
				n, pluralS(n), n*TileGfxSize),
		})
	}
	return diags, nil
}

// validityDiagnostics returns the warning-rule findings for a TA map.
func (m *Map) validityDiagnostics() []LintDiagnostic {
	var diags []LintDiagnostic
	if m.IsTAK {
		return diags
	}
	if n, first := m.countBadTileIndices(); n > 0 {
		diags = append(diags, LintDiagnostic{
			Rule:     LintRuleBadTileIndex,
			Severity: LintWarning,
			Count:    n,
			Message: fmt.Sprintf(
				"%d tile map cell%s reference tiles beyond the %d-tile set (first: index %d at tile %s); the game reads past its tile set there",
				n, pluralS(n), len(m.Tiles), m.TileMap[first], cellName(first, m.TileW)),
		})
	}
	count := m.featureTableSize()
	if n, first := m.countUnresolvedFeatures(count); n > 0 {
		diags = append(diags, LintDiagnostic{
			Rule:     LintRuleUnresolvedFeature,
			Severity: LintWarning,
			Count:    n,
			Message: fmt.Sprintf(
				"%d cell%s hold feature words beyond the %d-entry feature table (first: %d at cell %s); the game places nothing there",
				n, pluralS(n), count, m.TileAttr[first].Feature, cellName(first, m.AttrW)),
		})
	}
	if msgs := m.boundsWarnings(m.encodedSize(count)); len(msgs) > 0 {
		diags = append(diags, LintDiagnostic{
			Rule:     LintRuleInterchangeBounds,
			Severity: LintWarning,
			Count:    len(msgs),
			Message:  strings.Join(msgs, "; "),
		})
	}
	if msg := m.minimapWarning(); msg != "" {
		sev := LintWarning
		if m.MinimapW <= 0 || m.MinimapH <= 0 {
			sev = LintInfo
		}
		diags = append(diags, LintDiagnostic{Rule: LintRuleMinimap, Severity: sev, Count: 1, Message: msg})
	}
	return diags
}

// cellName formats grid index i of a grid w wide as "x,y".
func cellName(i, w int) string {
	if w <= 0 {
		return fmt.Sprint(i)
	}
	return fmt.Sprintf("%d,%d", i%w, i/w)
}

// clone returns a deep copy of m suitable for analysis without
// affecting the caller's data.  The feature table is not duplicated
// because Optimize never mutates it; Lint never writes back, so the
// copy only needs the fields Optimize touches.
func (m *Map) clone() *Map {
	cp := *m
	cp.TileMap = append([]uint16(nil), m.TileMap...)
	cp.TileAttr = append([]TileAttr(nil), m.TileAttr...)
	cp.Tiles = make([][]byte, len(m.Tiles))
	for i, t := range m.Tiles {
		cp.Tiles[i] = append([]byte(nil), t...)
	}
	if m.Minimap != nil {
		cp.Minimap = append([]byte(nil), m.Minimap...)
	}
	if m.MapDataPad != nil {
		cp.MapDataPad = append([]byte(nil), m.MapDataPad...)
	}
	return &cp
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
