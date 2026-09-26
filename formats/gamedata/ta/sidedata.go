package ta

import (
	"fmt"
	"sort"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// Side is a [SIDEn] section of gamedata/sidedata.tdf: the on-screen HUD layout
// and identity of one playable side (ARM, CORE). The many [LOGO], [ENERGYBAR],
// ... rectangle sub-sections are preserved generically in Regions. Fields shared
// with TA:Kingdoms live on the embedded common.SideBase.
//
// TA 3.1c reads the sides SIDE0, SIDE1, ... up to the first missing number,
// at most SideLimit of them (see GameSides), and needs every rectangle in
// SideRectangles in each side it reads (see CheckSides).
type Side struct {
	common.SideBase

	IntGAF      string `tdf:"intgaf,omitempty"`
	Font        string `tdf:"font,omitempty"`
	FontGUI     string `tdf:"fontgui,omitempty"`
	EnergyColor int    `tdf:"energycolor,omitempty"`
	MetalColor  int    `tdf:"metalcolor,omitempty"`

	// Regions holds the HUD rectangle sub-sections ([LOGO], [ENERGYBAR], ...),
	// each a { x1; y1; x2; y2; } block.
	Regions []common.Section `tdf:",sections"`
}

// Side satisfies the shared common.Side interface via its embedded base.
var _ common.Side = (*Side)(nil)

// SideData is the whole gamedata/sidedata.tdf document: the playable sides
// plus the [CANBUILD] construction table. (The lossless round-trip view stays
// []Side / []common.Section; this is the consumer-facing typed shape.)
type SideData struct {
	// Sides holds every section whose name starts with SIDE, in order. The
	// game reads only the ones GameSides returns.
	Sides []Side `tdf:"SIDE"`
	// CanBuild is the first [CANBUILD] section, the one the game reads.
	CanBuild CanBuild `tdf:"CANBUILD"`

	// Sections preserves every other top-level section, including a second
	// [CANBUILD], in order.
	Sections []common.Section `tdf:",sections"`

	// Remaining preserves any key=value outside the sections.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's section order and spelling (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// CanBuild is the [CANBUILD] table: one subsection per builder unit listing
// what it can construct. The game finds a builder's list with Builder and
// reads it with CanBuildBuilder.BuildList.
type CanBuild struct {
	Builders []CanBuildBuilder `tdf:",sections"`

	// Remaining preserves any key=value outside the builder subsections.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's section order and spelling (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// CanBuildBuilder is one builder's subsection: the section name is the
// builder unit (e.g. [ARMCOM]) and its canbuild1..N keys list the buildable
// units in menu order. The numbered keys stay in the dynamic map; BuildList
// reads them the way the game does.
type CanBuildBuilder struct {
	Name    string            `tdf:",name"`
	Entries map[string]string `tdf:",remaining"`

	// Sections preserves any section nested in the builder's subsection.
	Sections []common.Section `tdf:",sections"`

	// Meta records the order and text of the entries (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

const (
	// SideLimit is the most sides the game reads from sidedata.tdf.
	SideLimit = 5
	// SideNameMax, SideNamePrefixMax and SideCommanderMax are the longest
	// name, nameprefix and commander the game keeps for a side.
	SideNameMax       = 29
	SideNamePrefixMax = 3
	SideCommanderMax  = 31
	// CanBuildLimit is the most units a builder's [CANBUILD] list gives it.
	CanBuildLimit = 30
	// CanBuildNameMax is the longest canbuildN value the game keeps.
	CanBuildNameMax = 31
)

// SideRectangles names the rectangle sections the game reads from each side,
// in its order. A side the game reads must have every one of them, or the
// game loads no sides at all.
var SideRectangles = []string{
	"LOGO", "ENERGYBAR", "ENERGYNUM", "METALBAR", "METALNUM", "TOTALUNITS",
	"TOTALTIME", "ENERGY0", "METAL0", "ENERGYMAX", "METALMAX",
	"ENERGYPRODUCED", "ENERGYCONSUMED", "METALPRODUCED", "METALCONSUMED",
	"LOGO2", "UNITNAME", "DAMAGEBAR", "UNITMETALMAKE", "UNITMETALUSE",
	"UNITENERGYMAKE", "UNITENERGYUSE", "MISSIONTEXT", "UNITNAME2",
	"DAMAGEBAR2", "NAME", "DESCRIPTION", "RELOAD1", "RELOAD2", "RELOAD3",
}

// GameSides returns the sides the game reads, in side order: the first
// section named SIDE0 (ignoring case), then SIDE1, and so on, stopping at the
// first number with no such section and after SideLimit sides. Sections with
// other names (SIDE01, SIDE5, a SIDE3 after a gap) are never read.
func GameSides(sides []Side) []*Side {
	var out []*Side
	for n := 0; n < SideLimit; n++ {
		s := findSide(sides, n)
		if s == nil {
			break
		}
		out = append(out, s)
	}
	return out
}

// GameSides returns the sides the game reads (see the function GameSides).
func (d *SideData) GameSides() []*Side { return GameSides(d.Sides) }

func findSide(sides []Side, n int) *Side {
	want := fmt.Sprintf("SIDE%d", n)
	for i := range sides {
		if values.EqualFold(sides[i].Key, want) {
			return &sides[i]
		}
	}
	return nil
}

// Region returns the side's first rectangle section with this name, ignoring
// case, or nil.
func (s *Side) Region(name string) *common.Section {
	for i := range s.Regions {
		if values.EqualFold(s.Regions[i].Key, name) {
			return &s.Regions[i]
		}
	}
	return nil
}

// CheckSides reports what the game does not read, or refuses, in the sides of
// sidedata.tdf: sections it never reaches (a gap in the numbering, a sixth
// side, a name such as SIDE01), a side missing one of SideRectangles (the game
// then loads no sides at all) and text longer than it keeps.
func CheckSides(sides []Side) []common.Warning {
	var out []common.Warning
	read := map[*Side]bool{}
	for _, s := range GameSides(sides) {
		read[s] = true
	}
	for i := range sides {
		s := &sides[i]
		if !read[s] {
			out = append(out, common.Warning{Section: s.Key, Message: fmt.Sprintf(
				"the game reads only SIDE0 to SIDE%d, numbered without gaps, the first of each name", SideLimit-1)})
			continue
		}
		for _, r := range SideRectangles {
			if s.Region(r) == nil {
				out = append(out, common.Warning{Section: s.Key, Message: fmt.Sprintf(
					"no [%s] section; the game then loads no sides at all", r)})
			}
		}
		for _, f := range []struct {
			key, v string
			max    int
		}{{"name", s.Name, SideNameMax}, {"nameprefix", s.NamePrefix, SideNamePrefixMax}, {"commander", s.Commander, SideCommanderMax}} {
			if len(f.v) > f.max {
				out = append(out, common.Warning{Section: s.Key, Key: f.key, Message: fmt.Sprintf(
					"%d characters; the game keeps the first %d (%q)", len(f.v), f.max, values.Truncate(f.v, f.max))})
			}
		}
	}
	return out
}

// Check reports what the game does not read, or refuses, in the document: the
// sides (see CheckSides), a second [CANBUILD] section and builder lists with
// gaps or more entries than the game keeps.
func (d *SideData) Check() []common.Warning {
	out := CheckSides(d.Sides)
	for _, s := range d.Sections {
		if values.EqualFold(s.Key, "CANBUILD") {
			out = append(out, common.Warning{Section: s.Key, Message: "a second [CANBUILD]; the game reads the first"})
		}
	}
	seen := map[string]bool{}
	for i := range d.CanBuild.Builders {
		b := &d.CanBuild.Builders[i]
		section := "CANBUILD/" + b.Name
		folded := foldName(b.Name)
		if seen[folded] {
			out = append(out, common.Warning{Section: section, Message: "a second list for this builder; the game reads the first"})
			continue
		}
		seen[folded] = true
		length := b.listLength()
		for _, key := range sortedKeys(b.Entries) {
			if n, ok := canBuildIndex(key); ok && n > length {
				out = append(out, common.Warning{Section: section, Key: key, Message: fmt.Sprintf(
					"after a gap in the numbering (no canbuild%d); the game stops at the gap", length+1)})
			}
		}
		if total := length; total > CanBuildLimit {
			out = append(out, common.Warning{Section: section, Message: fmt.Sprintf(
				"%d entries; the game keeps the first %d units", total, CanBuildLimit)})
		}
	}
	return out
}

// canBuildIndex returns n for a key canbuildN (ignoring case) with N a plain
// positive number.
func canBuildIndex(key string) (int, bool) {
	if !values.HasPrefixFold(key, "canbuild") || len(key) == len("canbuild") {
		return 0, false
	}
	n := 0
	for _, c := range key[len("canbuild"):] {
		if c < '0' || c > '9' || n > 1<<20 {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, n > 0 && fmt.Sprintf("canbuild%d", n) == foldLower(key)
}

func foldLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// listLength counts canbuild1, canbuild2, ... up to the first missing key.
func (b *CanBuildBuilder) listLength() int {
	n := 0
	for {
		if _, ok := values.Lookup(b.Entries, fmt.Sprintf("canbuild%d", n+1)); !ok {
			return n
		}
		n++
	}
}

// Builder returns the builder's list the game reads: the first subsection
// whose name equals name, ignoring case, or nil.
func (c *CanBuild) Builder(name string) *CanBuildBuilder {
	for i := range c.Builders {
		if values.EqualFold(c.Builders[i].Name, name) {
			return &c.Builders[i]
		}
	}
	return nil
}

// BuildList returns the units the builder's list gives it, as the game reads
// it: the values of canbuild1, canbuild2, ... (keys compared ignoring case) up
// to the first missing key, each cut to CanBuildNameMax characters, in order.
// Empty values, and names known rejects when known is not nil (the game skips
// names that match no unit), are left out, and the list stops at
// CanBuildLimit units. The game reads a list only for a unit whose
// Builder key is set.
func (b *CanBuildBuilder) BuildList(known func(name string) bool) []string {
	var out []string
	for n := 1; len(out) < CanBuildLimit; n++ {
		v, ok := values.Lookup(b.Entries, fmt.Sprintf("canbuild%d", n))
		if !ok {
			break
		}
		v = values.Truncate(v, CanBuildNameMax)
		if v == "" || (known != nil && !known(v)) {
			continue
		}
		out = append(out, v)
	}
	return out
}
