package ta

import (
	"fmt"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
)

// MovementClass is one [CLASSn] section of gamedata/moveinfo.tdf. Decode the
// whole file with
//
//	var classes []ta.MovementClass
//	err := tdf.Unmarshal(data, &classes)
//
// Total Annihilation uses only the fields shared with TA:Kingdoms, so it adds
// nothing to the embedded common.MovementClassBase.
//
// TA 3.1c reads only the sections named CLASS0 to CLASS31 (MoveClassSlots
// sections, see MovementClassTable); a unit's MovementClass names a class by
// its name= key. Limits gives a class's values with the game's defaults.
type MovementClass struct {
	common.MovementClassBase
}

// MovementClass satisfies the shared common.MovementClass interface via its
// embedded base.
var _ common.MovementClass = (*MovementClass)(nil)

const (
	// MoveClassSlots is how many movement classes the game reads: the
	// sections CLASS0 to CLASS31 of moveinfo.tdf.
	MoveClassSlots = 32
	// MoveClassNameMax is the longest class name, and MovementClass value,
	// the game compares.
	MoveClassNameMax = 99

	// DefaultMaxWaterDepth, DefaultMinWaterDepth and DefaultSlope are what
	// the game uses for a movement key a class (or a unit with no class)
	// leaves out. A missing bad slope is half the maximum.
	DefaultMaxWaterDepth = 10000
	DefaultMinWaterDepth = -10000
	DefaultSlope         = 255
)

// MovementLimits is the footprint and terrain limits a unit moves with, as TA
// 3.1c resolves them: water depths kept to 16 bits, slopes to 8 bits.
type MovementLimits struct {
	FootprintX, FootprintZ       int
	MinWaterDepth, MaxWaterDepth int
	MaxSlope, BadSlope           int
	MaxWaterSlope, BadWaterSlope int
}

// intKey is one movement key's value and whether it was present.
type intKey struct {
	v       int32
	present bool
}

// resolveLimits applies the game's defaults, widths and clamps to the
// movement keys of a class section (or of a unit's own [UNITINFO]).
func resolveLimits(get func(key string) intKey) MovementLimits {
	or := func(key string, def int32) int32 {
		if k := get(key); k.present {
			return k.v
		}
		return def
	}
	var l MovementLimits
	l.FootprintX = int(int16(or("footprintx", 0)))
	l.FootprintZ = int(int16(or("footprintz", 0)))
	l.MaxWaterDepth = int(int16(or("maxwaterdepth", DefaultMaxWaterDepth)))
	l.MinWaterDepth = int(int16(or("minwaterdepth", DefaultMinWaterDepth)))
	maxSlope := uint32(or("maxslope", DefaultSlope))
	slope := uint8(maxSlope)
	bad := uint8(or("badslope", int32((maxSlope&0xff)>>1)))
	maxWater := uint32(or("maxwaterslope", DefaultSlope))
	waterSlope := uint8(maxWater)
	badWater := uint8(or("badwaterslope", int32((maxWater&0xff)>>1)))
	if waterSlope < slope {
		slope = waterSlope
	}
	if slope < bad {
		bad = slope
	}
	if waterSlope < badWater {
		badWater = waterSlope
	}
	l.MaxSlope, l.BadSlope = int(slope), int(bad)
	l.MaxWaterSlope, l.BadWaterSlope = int(waterSlope), int(badWater)
	return l
}

// Limits returns the class's limits as the game resolves them: a key the
// class leaves out takes the game's default (footprint 0, maximum water depth
// 10000, minimum -10000, slopes 255, a bad slope half its maximum), values
// are kept to the game's widths, and the maximum slope is capped by the
// maximum water slope, each bad slope by its maximum.
func (c *MovementClass) Limits() MovementLimits {
	b := &c.MovementClassBase
	typed := map[string]int{
		"footprintx": b.FootprintX, "footprintz": b.FootprintZ,
		"minwaterdepth": b.MinWaterDepth, "maxwaterdepth": b.MaxWaterDepth,
		"maxslope": b.MaxSlope, "maxwaterslope": b.MaxWaterSlope,
		"badslope": b.BadSlope, "badwaterslope": b.BadWaterSlope,
	}
	return resolveLimits(func(key string) intKey {
		v := typed[key]
		return intKey{int32(v), values.Present(&b.Meta, key, v != 0)}
	})
}

// MovementClassTable returns the classes the game reads, by slot: slot n is
// the first section named CLASSn (ignoring case), or nil when there is none.
// Other sections are never read.
func MovementClassTable(classes []MovementClass) [MoveClassSlots]*MovementClass {
	var table [MoveClassSlots]*MovementClass
	for slot := range table {
		want := fmt.Sprintf("CLASS%d", slot)
		for i := range classes {
			if values.EqualFold(classes[i].Key, want) {
				table[slot] = &classes[i]
				break
			}
		}
	}
	return table
}

// FindMovementClass returns the class a unit's MovementClass value names, as
// the game finds it: the lowest slot of MovementClassTable whose name matches,
// ignoring case (both compared on their first MoveClassNameMax characters). A
// class section with no name= key has an empty name. It returns nil and -1
// when no class matches.
func FindMovementClass(classes []MovementClass, name string) (*MovementClass, int) {
	name = values.Truncate(name, MoveClassNameMax)
	for slot, c := range MovementClassTable(classes) {
		if c != nil && values.EqualFold(values.Truncate(c.Name, MoveClassNameMax), name) {
			return c, slot
		}
	}
	return nil, -1
}

// CheckMoveInfo reports moveinfo.tdf sections the game never reads (names
// other than CLASS0..CLASS31, such as CLASS32 or CLASS01, and a second section
// of a slot), class names another slot shadows and names longer than the game
// compares.
func CheckMoveInfo(classes []MovementClass) []common.Warning {
	var out []common.Warning
	table := MovementClassTable(classes)
	used := map[*MovementClass]bool{}
	for _, c := range table {
		if c != nil {
			used[c] = true
		}
	}
	for i := range classes {
		c := &classes[i]
		if !used[c] {
			msg := fmt.Sprintf("the game reads only sections CLASS0 to CLASS%d, the first of each name", MoveClassSlots-1)
			out = append(out, common.Warning{Section: c.Key, Message: msg})
		}
	}
	names := map[string]int{}
	for slot, c := range table {
		if c == nil {
			continue
		}
		if len(c.Name) > MoveClassNameMax {
			out = append(out, common.Warning{Section: c.Key, Key: "name", Message: fmt.Sprintf(
				"%d characters; the game compares the first %d", len(c.Name), MoveClassNameMax)})
		}
		folded := foldName(values.Truncate(c.Name, MoveClassNameMax))
		if first, ok := names[folded]; ok {
			out = append(out, common.Warning{Section: c.Key, Key: "name", Message: fmt.Sprintf(
				"%q is also the name of CLASS%d, which units find first", c.Name, first)})
			continue
		}
		names[folded] = slot
	}
	return out
}

// foldName upper-cases ASCII letters only, as the game's name comparisons do.
func foldName(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
