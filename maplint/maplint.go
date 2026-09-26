// Package maplint runs the studio's quality checks against a parsed
// TNT map + OTA metadata, returning a list of diagnostics.  It's the
// shared implementation behind both `kbot studio`'s Quality Checker
// dialog and the `kbot tnt lint` CLI / MCP entry points.
//
// The package is deliberately framework-agnostic: callers build an
// Input value (tnt.Map plus a small set of neutral structs) and get
// back []Diagnostic.  No HTTP, no JSON, no studio-specific types.
//
// The constants at the top tune the heuristics; the studio's UI
// references the same values when wrapping diagnostics with auto-fix
// metadata.
//
// The OTA checks follow TA 3.1c's rules. ParseOTA reads the file with the
// game's grammar and keeps only the schemas the game can find (Schema 0,
// Schema 1, ... up to the first missing number) and every start position
// the game places, numbered its way (StartPos0 and unnumbered entries
// included). CheckSchemaSlotsVsPlayers picks, for each advertised player
// count, the schema a skirmish or multiplayer game would use: only Network 1
// to Network 4 schemas take part (see MultiplayerSchema).
package maplint

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/ta"
	"github.com/coreprime/kbot-io/formats/tnt"
)

// Tunable thresholds shared across all callers.  Documented here so a
// future maintainer can change behaviour in one place.
const (
	// VoidFeature — the canonical sentinel TA writes into
	// TileAttr.Feature for impassable / void cells.  0xFFFF means "no
	// feature, passable"; 0xFFFE / 0xFFFD also appear in the wild on a
	// handful of early Cavedog maps (Metal Heck has 724 of them on
	// what are clearly buildable steam-vent cells, and Lava Run has a
	// similar pattern) — their semantics are not "void" in the engine
	// and we treat them as "no feature" defensively.
	VoidFeature = uint16(0xFFFC)

	// MetalProximityTiles — a start position is flagged when its
	// nearest metal-producing feature is further than this many
	// tiles (1 tile = 32 game-pixels, 1 attribute cell = 16 px).
	// 24 tiles ≈ 1.5× commander beam reach, generous early-game
	// expansion range.
	MetalProximityTiles = 24

	// HeightDiscontinuityThreshold — adjacent attribute cells with
	// |Δheight| beyond this read as cliffs that walking units
	// can't traverse.  TA's units can step ~16 height per attr
	// cell; 32 is double that and a reliable "this looks broken"
	// floor.
	HeightDiscontinuityThreshold = 32

	// MetalRichSurfaceThreshold — when a schema's SurfaceMetal is
	// at or above this value the map is considered metal-rich
	// (Metal Heck uses 255).  In that mode the engine extracts
	// metal from open ground via mexes anywhere, so the
	// metal-proximity check skips that schema's starts.
	MetalRichSurfaceThreshold = 8

	// VoidIslandsTolerance — flood-fill from starts will routinely
	// strand a few cells in tight corners or behind feature
	// footprints.  Anything under this count is treated as
	// acceptable noise and the check stays green.
	VoidIslandsTolerance = 20
)

// Severity is a coarse traffic light used by both the studio dialog
// and the CLI output.
type Severity string

const (
	SeverityOK      Severity = "ok"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Diagnostic is one result row.  ID is a stable, machine-readable
// identifier for the rule; Label is a short human title; Message is
// the per-run description ("12 cells stranded" etc.).
type Diagnostic struct {
	ID       string
	Label    string
	Severity Severity
	Message  string
}

// StartPos mirrors TA's per-schema spawn point — game-pixel
// coordinates (not attribute cells).
type StartPos struct {
	// Number is the number the game gives the start position (StartPos3
	// is 3; see ta.StartPosition for entries with no number).
	Number int
	X, Z   int
	// Slot is the player slot the game places at this position: Number-1,
	// or 0 for StartPos0. ParseOTA fills it; callers building StartPos
	// values themselves may leave it 0.
	Slot int
}

// SchemaInfo carries the schema fields the checks read.  The studio
// adapter pulls these from its saveRequest's nested otaSchema; the
// CLI builds them by parsing an .ota file.
//
// The checks treat a list of schemas as the game's Schema 0, Schema 1, ...
// in order, and Type as the schema's type: skirmish and multiplayer games
// use only Network 1 to Network 4 schemas.
type SchemaInfo struct {
	Name         string
	Type         string
	SurfaceMetal int
	StartPos     []StartPos
}

// OTAInfo is the subset of an .ota file the lint inspects.
type OTAInfo struct {
	MissionName        string
	MissionDescription string
	Planet             string
	NumPlayers         string
	Size               string
	SeaLevel           int
	Schemas            []SchemaInfo
	// UnreachableSchemas names schema sections the game never reads (after
	// a gap in the numbering, or spelled otherwise than "Schema N").
	UnreachableSchemas []string
}

// FeaturePlacement records where a named feature sits on the map in
// attribute-cell coordinates (16 game pixels per cell).
type FeaturePlacement struct {
	Name string
	AX   int
	AY   int
}

// Input bundles everything the lint needs.  Map is required; the
// remaining fields are optional but unlock the corresponding checks.
//   - OTA nil → schema, start-position, metal-proximity, metadata
//     checks all skip with an "ok" message.
//   - FeatureRegistry nil → metal-proximity check skips with a
//     "feature library unavailable" note.
//
// AppliedFixes lets the caller short-circuit individual rules once
// their fix has been applied (e.g. compressTiles disables the
// duplicate-tiles report).
type Input struct {
	Map             *tnt.Map
	OTA             *OTAInfo
	Features        []FeaturePlacement
	FeatureRegistry map[string]int // lowercased feature name → metal yield (0 = non-metal)
	AppliedFixes    []string
}

// Run executes every check against the supplied Input and returns
// diagnostics in a stable order (the studio's UI relies on it).
func Run(in Input) []Diagnostic {
	return []Diagnostic{
		CheckDuplicateTiles(in),
		CheckMissingOTAFields(in),
		CheckStartPositionsInBounds(in),
		CheckSchemaSlotsVsPlayers(in),
		CheckMetalProximity(in),
		CheckVoidIslands(in),
		CheckHeightDiscontinuities(in),
	}
}

// ── Individual checks ──────────────────────────────────────────────────────

// CheckDuplicateTiles flags any byte-identical entries in the TNT
// tile pool.  When the "compressTiles" fix has already been applied
// the pool is dedup-by-construction, so we shortcut to OK without
// re-hashing.
func CheckDuplicateTiles(in Input) Diagnostic {
	const id = "dedupTiles"
	const label = "Deduplicate Tiles"
	const fixID = "compressTiles"
	m := in.Map
	if m == nil {
		return ok(id, label, "No map loaded.")
	}
	if hasApplied(in.AppliedFixes, fixID) {
		return ok(id, label, fmt.Sprintf("All %d tiles are unique.", len(m.Tiles)))
	}
	seen := make(map[[1024]byte]bool, len(m.Tiles))
	dups := 0
	for _, t := range m.Tiles {
		if len(t) < 1024 {
			continue
		}
		var key [1024]byte
		copy(key[:], t)
		if seen[key] {
			dups++
		} else {
			seen[key] = true
		}
	}
	if dups == 0 {
		return ok(id, label, fmt.Sprintf("All %d tiles are unique.", len(m.Tiles)))
	}
	return Diagnostic{
		ID: id, Label: label, Severity: SeverityWarning,
		Message: fmt.Sprintf("%d duplicate tiles. Compress to %d distinct.", dups, len(seen)),
	}
}

// CheckStartPositionsInBounds confirms every schema's start
// positions land on a passable attribute cell that's inside the map.
func CheckStartPositionsInBounds(in Input) Diagnostic {
	const id = "startsInBounds"
	const label = "Reachable Start Positions"
	if in.Map == nil || in.OTA == nil || len(in.OTA.Schemas) == 0 {
		return ok(id, label, "No schemas to check.")
	}
	m := in.Map
	attrW, attrH := m.AttrW, m.AttrH
	var bad []string
	for si, s := range in.OTA.Schemas {
		for _, sp := range s.StartPos {
			ax := sp.X / 16
			ay := sp.Z / 16
			if ax < 0 || ay < 0 || ax >= attrW || ay >= attrH {
				bad = append(bad, fmt.Sprintf("Schema %d / StartPos%d (out of bounds)", si+1, sp.Number))
				continue
			}
			a := m.TileAttr[ay*attrW+ax]
			if a.Feature == VoidFeature {
				bad = append(bad, fmt.Sprintf("Schema %d / StartPos%d (in void)", si+1, sp.Number))
			}
		}
	}
	if len(bad) == 0 {
		return ok(id, label, "Every start position lands on passable ground.")
	}
	return Diagnostic{
		ID: id, Label: label, Severity: SeverityWarning,
		Message: fmt.Sprintf("%d unreachable: %s", len(bad), joinTop(bad, 3)),
	}
}

// CheckMetalProximity walks each start and reports any whose nearest
// metal-producing feature exceeds MetalProximityTiles.  Schemas
// configured as metal-rich (SurfaceMetal ≥ threshold) skip the check.
func CheckMetalProximity(in Input) Diagnostic {
	const id = "metalProximity"
	const label = "Metal Near Starts"
	if in.OTA == nil || len(in.OTA.Schemas) == 0 {
		return ok(id, label, "No schemas to check.")
	}
	type checkable struct {
		idx    int
		schema SchemaInfo
	}
	var toCheck []checkable
	for si, s := range in.OTA.Schemas {
		if s.SurfaceMetal >= MetalRichSurfaceThreshold {
			continue
		}
		toCheck = append(toCheck, checkable{si, s})
	}
	if len(toCheck) == 0 {
		return ok(id, label, fmt.Sprintf("All schemas are metal-rich (SurfaceMetal ≥ %d) — proximity not required.", MetalRichSurfaceThreshold))
	}
	if in.FeatureRegistry == nil {
		return ok(id, label, "Feature registry unavailable — skipping metal proximity.")
	}
	type pt struct{ x, y float64 }
	var metals []pt
	for _, f := range in.Features {
		yield, ok := in.FeatureRegistry[strings.ToLower(f.Name)]
		if !ok || yield <= 0 {
			continue
		}
		// Attribute cells → tile units (1 tile = 2 attr cells).
		metals = append(metals, pt{x: float64(f.AX) / 2, y: float64(f.AY) / 2})
	}
	if len(metals) == 0 {
		return Diagnostic{
			ID: id, Label: label, Severity: SeverityWarning,
			Message: "No metal-producing features placed anywhere on the map.",
		}
	}
	limitSq := float64(MetalProximityTiles * MetalProximityTiles)
	var bad []string
	for _, c := range toCheck {
		for _, sp := range c.schema.StartPos {
			sx := float64(sp.X) / 32
			sy := float64(sp.Z) / 32
			nearestSq := math.MaxFloat64
			for _, m := range metals {
				dx := sx - m.x
				dy := sy - m.y
				d := dx*dx + dy*dy
				if d < nearestSq {
					nearestSq = d
				}
			}
			if nearestSq > limitSq {
				bad = append(bad, fmt.Sprintf("Schema %d / StartPos%d (%dt away)", c.idx+1, sp.Number, int(math.Sqrt(nearestSq))))
			}
		}
	}
	if len(bad) == 0 {
		return ok(id, label, fmt.Sprintf("All starts have metal within %d tiles.", MetalProximityTiles))
	}
	return Diagnostic{
		ID: id, Label: label, Severity: SeverityWarning,
		Message: fmt.Sprintf("%d short on metal: %s", len(bad), joinTop(bad, 3)),
	}
}

// CheckVoidIslands flood-fills from every start over the passable
// attribute grid and counts cells the fill never reached.
func CheckVoidIslands(in Input) Diagnostic {
	const id = "voidIslands"
	const label = "Connected Land"
	if in.Map == nil || in.OTA == nil || len(in.OTA.Schemas) == 0 {
		return ok(id, label, "No schemas to check.")
	}
	m := in.Map
	w, h := m.AttrW, m.AttrH
	passable := make([]bool, w*h)
	totalPassable := 0
	for i, a := range m.TileAttr {
		if a.Feature != VoidFeature {
			passable[i] = true
			totalPassable++
		}
	}
	visited := make([]bool, w*h)
	queue := make([]int, 0, 256)
	push := func(ax, ay int) {
		if ax < 0 || ay < 0 || ax >= w || ay >= h {
			return
		}
		idx := ay*w + ax
		if !passable[idx] || visited[idx] {
			return
		}
		visited[idx] = true
		queue = append(queue, idx)
	}
	for _, s := range in.OTA.Schemas {
		for _, sp := range s.StartPos {
			push(sp.X/16, sp.Z/16)
		}
	}
	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		ax := idx % w
		ay := idx / w
		push(ax+1, ay)
		push(ax-1, ay)
		push(ax, ay+1)
		push(ax, ay-1)
	}
	stranded := 0
	for i := range passable {
		if passable[i] && !visited[i] {
			stranded++
		}
	}
	if stranded == 0 {
		return ok(id, label, "All passable cells are reachable from a start.")
	}
	if stranded < VoidIslandsTolerance {
		return ok(id, label, fmt.Sprintf("%d cell(s) stranded — within tolerance (<%d).", stranded, VoidIslandsTolerance))
	}
	pct := 0.0
	if totalPassable > 0 {
		pct = float64(stranded) * 100 / float64(totalPassable)
	}
	return Diagnostic{
		ID: id, Label: label, Severity: SeverityWarning,
		Message: fmt.Sprintf("%d cells (%.1f%%) stranded behind voids — unreachable from any start.", stranded, pct),
	}
}

// CheckHeightDiscontinuities counts adjacent attribute cell pairs
// whose height delta exceeds HeightDiscontinuityThreshold.
func CheckHeightDiscontinuities(in Input) Diagnostic {
	const id = "heightDiscontinuities"
	const label = "Heightmap Smoothness"
	if in.Map == nil {
		return ok(id, label, "No map loaded.")
	}
	m := in.Map
	w, h := m.AttrW, m.AttrH
	if w == 0 || h == 0 {
		return ok(id, label, "Empty heightmap.")
	}
	cliffs := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cur := int(m.TileAttr[y*w+x].Height)
			if x+1 < w {
				d := cur - int(m.TileAttr[y*w+x+1].Height)
				if d < 0 {
					d = -d
				}
				if d > HeightDiscontinuityThreshold {
					cliffs++
				}
			}
			if y+1 < h {
				d := cur - int(m.TileAttr[(y+1)*w+x].Height)
				if d < 0 {
					d = -d
				}
				if d > HeightDiscontinuityThreshold {
					cliffs++
				}
			}
		}
	}
	if cliffs == 0 {
		return ok(id, label, fmt.Sprintf("No cliff edges over %d height units.", HeightDiscontinuityThreshold))
	}
	return Diagnostic{
		ID: id, Label: label, Severity: SeverityWarning,
		Message: fmt.Sprintf("%d cliff edges over %d height units — may block ground pathing.", cliffs, HeightDiscontinuityThreshold),
	}
}

// CheckMissingOTAFields walks the lobby-displayed metadata and flags
// blank required fields.
func CheckMissingOTAFields(in Input) Diagnostic {
	const id = "otaFields"
	const label = "Required Metadata"
	if in.OTA == nil {
		return Diagnostic{
			ID: id, Label: label, Severity: SeverityWarning,
			Message: "No .ota metadata supplied.",
		}
	}
	missing := []string{}
	push := func(name, val string) {
		if strings.TrimSpace(val) == "" {
			missing = append(missing, name)
		}
	}
	push("Mission name", in.OTA.MissionName)
	push("Description", in.OTA.MissionDescription)
	push("Planet", in.OTA.Planet)
	push("Players supported", in.OTA.NumPlayers)
	push("Size", in.OTA.Size)
	if len(in.OTA.Schemas) == 0 {
		missing = append(missing, "At least one schema")
	}
	if len(missing) == 0 {
		return ok(id, label, "Mission name, planet, size, and schemas all set.")
	}
	return Diagnostic{
		ID: id, Label: label, Severity: SeverityWarning,
		Message: "Missing: " + strings.Join(missing, ", "),
	}
}

// CheckSchemaSlotsVsPlayers verifies that the game can host every player
// count numplayers advertises. For each count it picks the schema the game
// would use (see MultiplayerSchema) and flags a count whose schema has fewer
// start positions than players, a map with no Network 1 to Network 4 schema
// with start positions (skirmish and multiplayer games cannot use it) and
// schema sections the game never reads.
func CheckSchemaSlotsVsPlayers(in Input) Diagnostic {
	const id = "schemaSlots"
	const label = "Schema Player Slots"
	if in.OTA == nil || (len(in.OTA.Schemas) == 0 && len(in.OTA.UnreachableSchemas) == 0) {
		return ok(id, label, "No schemas to check.")
	}
	var problems []string
	if len(in.OTA.UnreachableSchemas) > 0 {
		problems = append(problems, fmt.Sprintf("the game never reads %s (it reads Schema 0, Schema 1, ... up to the first missing number)",
			strings.Join(in.OTA.UnreachableSchemas, ", ")))
	}
	schemas := in.OTA.Schemas
	counts := ParsePlayerCounts(in.OTA.NumPlayers)
	var missing []string
	if MultiplayerSchema(schemas, 0) < 0 {
		problems = append(problems, "no Network 1 to Network 4 schema has start positions, so skirmish and multiplayer games cannot use the map")
	} else {
		for _, n := range counts {
			i := MultiplayerSchema(schemas, n)
			if have := len(schemas[i].StartPos); have < n {
				missing = append(missing, strconv.Itoa(n))
				problems = append(problems, fmt.Sprintf("for %d players the game uses schema %d (%s), which has %d start positions",
					n, i, schemas[i].Type, have))
			}
		}
	}
	if len(problems) == 0 {
		if len(counts) == 0 {
			return ok(id, label, "A Network schema with start positions is available.")
		}
		return ok(id, label, fmt.Sprintf("Schemas cover every player count (%s).", strings.Join(intsToStrings(counts), ", ")))
	}
	msg := strings.Join(problems, "; ")
	if len(missing) > 0 {
		msg = fmt.Sprintf("No schema has enough starts for player count(s): %s — %s", strings.Join(missing, ", "), msg)
	}
	return Diagnostic{ID: id, Label: label, Severity: SeverityWarning, Message: msg}
}

// MultiplayerSchema returns the index in schemas of the schema a skirmish or
// multiplayer game for this many players (0 for no particular number) uses,
// or -1, choosing as the game does: it tries the types Network 1 to Network 4
// in turn, each over the schemas in order; a schema qualifies when its Type
// matches (ignoring case) and it has start positions. The last qualifying
// schema with exactly players start positions wins; until one is found, each
// qualifying schema with more start positions than the best so far replaces
// it. schemas are taken as the game's Schema 0, Schema 1, ... in order.
func MultiplayerSchema(schemas []SchemaInfo, players int) int {
	best, bestCount := -1, 0
	for _, typ := range ta.NetworkSchemaTypes {
		for i, s := range schemas {
			t := s.Type
			if len(t) > 31 {
				t = t[:31]
			}
			if !asciiEqualFold(t, typ) {
				continue
			}
			count := len(s.StartPos)
			if count != 0 && (count == players || players == 0 || (bestCount < count && bestCount != players)) {
				best, bestCount = i, count
			}
		}
	}
	return best
}

func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'a' <= x && x <= 'z' {
			x -= 'a' - 'A'
		}
		if 'a' <= y && y <= 'z' {
			y -= 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// ── Helpers exported for callers that need to reuse the parsing ────────────

// ParsePlayerCounts reads the player counts a numplayers string like "2, 3,
// 4" advertises, leniently: every number in it, with ranges such as "2-8"
// expanded (see common.ParsePlayerCounts). The game only shows the text.
func ParsePlayerCounts(s string) []int {
	return common.ParsePlayerCounts(s)
}

// ── Internal helpers ───────────────────────────────────────────────────────

func ok(id, label, msg string) Diagnostic {
	return Diagnostic{ID: id, Label: label, Severity: SeverityOK, Message: msg}
}

func hasApplied(applied []string, id string) bool {
	for _, a := range applied {
		if a == id {
			return true
		}
	}
	return false
}

func joinTop(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", +%d more", len(items)-n)
}

func intsToStrings(xs []int) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strconv.Itoa(x)
	}
	return out
}
