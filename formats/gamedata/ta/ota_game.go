package ta

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

const (
	// MaxSchemas is the most schemas the game looks up: Schema 0 to
	// Schema 63.
	MaxSchemas = 64
	// DefaultMaxUnits is a campaign mission's unit limit per player when its
	// header has no maxunits key. Skirmish and multiplayer games do not read
	// maxunits.
	DefaultMaxUnits = 200
	// DefaultMissionDescription is what the game shows for a map whose
	// header has no missiondescription key.
	DefaultMissionDescription = "No description available"
	// DefaultHealthPercentage is the health of a pre-placed unit with no
	// HealthPercentage key.
	DefaultHealthPercentage = 100
	// StartPosPrefix begins the SpecialWhat value of a start position,
	// compared ignoring case.
	StartPosPrefix = "StartPos"
)

// NetworkSchemaTypes are the schema types a skirmish or multiplayer game can
// use, in the order the game tries them. CampaignSchemaTypes are a campaign
// mission's, by difficulty.
var (
	NetworkSchemaTypes  = []string{"Network 1", "Network 2", "Network 3", "Network 4"}
	CampaignSchemaTypes = []string{"Easy", "Medium", "Hard"}
)

// ErrNoGlobalHeader is returned by ReadMap for text with no [GlobalHeader]
// section, which the game refuses as a map.
var ErrNoGlobalHeader = errors.New("ta: no [GlobalHeader] section")

// ReadMap decodes an .ota file. It fails for text with no [GlobalHeader]
// section, which tdf.Unmarshal accepts as an empty map. Use Check to find what
// the game ignores, such as a TA: Kingdoms [Map Data] section.
func ReadMap(data []byte) (*Map, error) {
	var m Map
	if err := tdf.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if !m.Meta.HasSection("GlobalHeader") {
		return nil, ErrNoGlobalHeader
	}
	return &m, nil
}

// schemaStem is the tag key of GlobalHeader.Schemas: Marshal names an unnamed
// schema with it and a number.
const schemaStem = "Schema "

// schemaNames returns the names Marshal writes the schemas under: each Key,
// or for an empty Key "Schema N" with N the lowest number no other schema is
// named with (see tdf.ElementNames).
func (h *GlobalHeader) schemaNames() []string {
	names := make([]string, len(h.Schemas))
	for i := range h.Schemas {
		names[i] = h.Schemas[i].Key
	}
	return tdf.ElementNames(schemaStem, names)
}

// findSchema returns the index of the first of names equal to "Schema n",
// ignoring case, or -1.
func findSchema(names []string, n int) int {
	want := schemaStem + strconv.Itoa(n)
	for i, name := range names {
		if values.EqualFold(name, want) {
			return i
		}
	}
	return -1
}

// Schema returns the schema the game finds as "Schema n": the first of
// Schemas whose name equals it ignoring case, or nil. Names are compared
// exactly otherwise, so [Schema 00] or [Schema  1] is never found. A schema
// with an empty Key counts under the name Marshal writes for it (see
// Schemas).
func (h *GlobalHeader) Schema(n int) *Schema {
	if i := findSchema(h.schemaNames(), n); i >= 0 {
		return &h.Schemas[i]
	}
	return nil
}

// GameSchemas returns the schemas the game can use, in number order: Schema 0,
// Schema 1, ... up to the first number with no such section, at most
// MaxSchemas.
func (h *GlobalHeader) GameSchemas() []*Schema {
	names := h.schemaNames()
	var out []*Schema
	for n := 0; n < MaxSchemas; n++ {
		i := findSchema(names, n)
		if i < 0 {
			break
		}
		out = append(out, &h.Schemas[i])
	}
	return out
}

// UnreachableSchemas returns the names of schema sections the game never
// reads: entries of Schemas after a gap in the numbering, with another
// spelling of a number (Schema 00) or repeating a name, and header sections
// such as [Schema0] whose names start with "Schema" without the space.
func (h *GlobalHeader) UnreachableSchemas() []string {
	reached := map[*Schema]bool{}
	for _, s := range h.GameSchemas() {
		reached[s] = true
	}
	var out []string
	for i, name := range h.schemaNames() {
		if !reached[&h.Schemas[i]] {
			out = append(out, name)
		}
	}
	for _, s := range h.Sections {
		if values.HasPrefixFold(s.Key, "schema") {
			out = append(out, s.Key)
		}
	}
	return out
}

// AddSchema appends s, naming it "Schema N" when its Key is empty, with N the
// lowest number no schema uses, and returns a pointer to the stored schema. It
// sets SCHEMACOUNT to the new number of schemas when the header has that key
// or was decoded from a file (a header built in code gets the key when it is
// written).
func (h *GlobalHeader) AddSchema(s Schema) *Schema {
	h.Schemas = append(h.Schemas, s)
	last := &h.Schemas[len(h.Schemas)-1]
	names := h.schemaNames()
	last.Key = names[len(names)-1]
	h.syncSchemaCount()
	return last
}

// RemoveSchema removes the schema the game finds as "Schema n" (see Schema)
// and renumbers the rest with RenumberSchemas, so the game still finds every
// one. It reports whether there was such a schema; without one nothing
// changes.
func (h *GlobalHeader) RemoveSchema(n int) bool {
	i := findSchema(h.schemaNames(), n)
	if i < 0 {
		return false
	}
	h.Schemas = append(h.Schemas[:i:i], h.Schemas[i+1:]...)
	h.RenumberSchemas()
	return true
}

// RenumberSchemas names the schemas Schema 0, Schema 1, ... in order, so the
// game finds every one of them (up to MaxSchemas), and sets SCHEMACOUNT as
// AddSchema does. The schemas are first put in the order of the numbers they
// are named with (as Marshal would write them), so the ones the game already
// finds keep their numbers and order; schemas whose names the game cannot
// find under any number (such as Schema 00), and later schemas with a number
// already taken, follow in their current order. Afterwards Schemas[i] is Schema i, and pointers
// taken into Schemas before the call may point at other schemas.
func (h *GlobalHeader) RenumberSchemas() {
	names := h.schemaNames()
	type ranked struct {
		s   Schema
		n   int
		num bool // named with a number the game can find, first of that number
	}
	seen := map[int]bool{}
	list := make([]ranked, len(h.Schemas))
	for i, name := range names {
		n, ok := schemaNumber(name)
		list[i] = ranked{s: h.Schemas[i], n: n, num: ok && !seen[n]}
		if ok {
			seen[n] = true
		}
	}
	sort.SliceStable(list, func(a, b int) bool {
		if list[a].num != list[b].num {
			return list[a].num
		}
		return list[a].num && list[a].n < list[b].n
	})
	for i := range list {
		list[i].s.Key = schemaStem + strconv.Itoa(i)
		h.Schemas[i] = list[i].s
	}
	h.syncSchemaCount()
}

// schemaNumber returns N for a name the game can find as "Schema N": the
// prefix in any case, then N written as a plain decimal number without
// leading zeros.
func schemaNumber(name string) (int, bool) {
	if !values.HasPrefixFold(name, schemaStem) {
		return 0, false
	}
	digits := name[len(schemaStem):]
	if len(digits) > 9 {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || strconv.Itoa(n) != digits {
		return 0, false
	}
	return n, true
}

// syncSchemaCount sets SCHEMACOUNT to the number of schemas when the header
// has the key, spelled as it is, or was decoded from a file without it.
func (h *GlobalHeader) syncSchemaCount() {
	count := strconv.Itoa(len(h.Schemas))
	for k := range h.Remaining {
		if values.EqualFold(k, "schemacount") {
			h.Remaining[k] = count
			return
		}
	}
	if h.Meta.Decoded() {
		if h.Remaining == nil {
			h.Remaining = map[string]string{}
		}
		h.Remaining["SCHEMACOUNT"] = count
	}
}

// MultiplayerSchema returns the schema a skirmish or multiplayer game for this
// many players uses (0 for no particular number), as the game chooses it, or
// nil when there is none. The game tries the types of NetworkSchemaTypes in
// order, each over GameSchemas; a schema qualifies when its Type matches
// (ignoring case) and it has a [specials] section with at least one start
// position. The last qualifying schema with exactly players start positions
// wins; until one is found, each qualifying schema with more start positions
// than the best so far replaces it. With players 0 the last qualifying schema
// wins.
func (h *GlobalHeader) MultiplayerSchema(players int) *Schema {
	var best *Schema
	bestCount := 0
	schemas := h.GameSchemas()
	for _, typ := range NetworkSchemaTypes {
		for _, s := range schemas {
			if !values.EqualFold(values.Truncate(s.Type, 31), typ) || s.Specials == nil {
				continue
			}
			count := len(s.StartPositions())
			if count != 0 && (count == players || players == 0 || (bestCount < count && bestCount != players)) {
				best, bestCount = s, count
			}
		}
	}
	return best
}

// CampaignSchema returns the schema a campaign mission uses at a difficulty (0
// easy, 1 medium, 2 hard), or nil: the first of GameSchemas whose Type is the
// difficulty's, else the nearest other difficulty's (medium then hard for
// easy, easy then hard for medium, medium then easy for hard).
func (h *GlobalHeader) CampaignSchema(difficulty int) *Schema {
	var order []int
	switch difficulty {
	case 0:
		order = []int{0, 1, 2}
	case 1:
		order = []int{1, 0, 2}
	case 2:
		order = []int{2, 1, 0}
	default:
		return nil
	}
	schemas := h.GameSchemas()
	for _, t := range order {
		for _, s := range schemas {
			if values.EqualFold(values.Truncate(s.Type, 31), CampaignSchemaTypes[t]) {
				return s
			}
		}
	}
	return nil
}

// EffectiveTidalStrength returns tidalstrength as the game reads it, a
// fraction where TidalStrength holds only its whole part.
func (h *GlobalHeader) EffectiveTidalStrength() float64 {
	return values.Float(&h.Meta, h.Remaining, "tidalstrength", h.TidalStrength)
}

// EffectiveKillMul returns killmul as the game reads it, a fraction.
func (h *GlobalHeader) EffectiveKillMul() float64 {
	return values.Float(&h.Meta, h.Remaining, "killmul", h.KillMul)
}

// EffectiveTimeMul returns timemul as the game reads it, a fraction.
func (h *GlobalHeader) EffectiveTimeMul() float64 {
	return values.Float(&h.Meta, h.Remaining, "timemul", h.TimeMul)
}

// EffectiveMaxUnits returns a campaign mission's unit limit: MaxUnits, or
// DefaultMaxUnits when the key is missing (an explicit 0 stays 0).
func (h *GlobalHeader) EffectiveMaxUnits() int {
	if values.Present(&h.Meta, "maxunits", h.MaxUnits != 0) {
		return h.MaxUnits
	}
	return DefaultMaxUnits
}

// EffectiveMissionDescription returns the description the game shows:
// MissionDescription, or DefaultMissionDescription when the key is missing.
func (h *GlobalHeader) EffectiveMissionDescription() string {
	if values.Present(&h.Meta, "missiondescription", h.MissionDescription != "") {
		return h.MissionDescription
	}
	return DefaultMissionDescription
}

// EffectiveMeteorDuration returns the meteor duration as the game reads it, a
// fraction where MeteorDuration holds only its whole part.
func (s *Schema) EffectiveMeteorDuration() float64 {
	return values.Float(&s.Meta, s.Remaining, "meteorduration", s.MeteorDuration)
}

// EffectiveMeteorInterval returns the meteor interval as the game reads it, a
// fraction where MeteorInterval holds only its whole part.
func (s *Schema) EffectiveMeteorInterval() float64 {
	return values.Float(&s.Meta, s.Remaining, "meteorinterval", s.MeteorInterval)
}

// SetMeteorDuration sets meteorduration to v, which may be fractional: Marshal
// writes it exactly and MeteorDuration holds its whole part.
func (s *Schema) SetMeteorDuration(v float64) {
	values.SetFloat(&s.Meta, &s.Remaining, "meteorduration", &s.MeteorDuration, v)
}

// SetMeteorInterval sets meteorinterval to v (see SetMeteorDuration).
func (s *Schema) SetMeteorInterval(v float64) {
	values.SetFloat(&s.Meta, &s.Remaining, "meteorinterval", &s.MeteorInterval, v)
}

// Meteors returns the meteor shower a game on this schema runs. With no
// MeteorWeapon there is none (Enabled false). Otherwise the schema's own
// settings apply, unless its radius, density, duration or interval is 0 (or
// missing), in which case all of defaults applies instead: the [Default]
// section of gamedata/meteor.tdf, which may be nil.
func (s *Schema) Meteors(defaults *Meteor) MeteorSettings {
	fallback := MeteorSettings{}
	if defaults != nil {
		fallback = defaults.settings()
	}
	if s.MeteorWeapon == "" {
		fallback.Enabled = false
		return fallback
	}
	own := MeteorSettings{
		Enabled:  true,
		Weapon:   s.MeteorWeapon,
		Radius:   s.MeteorRadius,
		Density:  s.MeteorDensity,
		Duration: s.EffectiveMeteorDuration(),
		Interval: s.EffectiveMeteorInterval(),
	}
	if own.Radius == 0 || float32(own.Density) == 0 || float32(own.Duration) == 0 || float32(own.Interval) == 0 {
		if defaults != nil {
			fallback.Enabled = true
			return fallback
		}
	}
	return own
}

// StartPosition is one start position of a schema, as the game numbers it.
type StartPosition struct {
	// Number is the number after "StartPos" in SpecialWhat, or for an
	// entry with no number (its text after StartPos does not start with a
	// digit), one more than the previous such entry's (the first is 1).
	Number int
	// Slot is the player slot the position belongs to: Number-1, so
	// StartPos1 is slot 0, and 0 for StartPos0.
	Slot int
	// X and Z are XPos and ZPos kept to 16 bits, as the game stores them.
	X, Z int
	// Special is the entry the position was read from.
	Special *Special
}

// StartPositions returns the schema's start positions in [specials] order:
// every entry whose SpecialWhat starts with StartPosPrefix (ignoring case),
// numbered as the game numbers them (see StartPosition).
func (s *Schema) StartPositions() []StartPosition {
	if s.Specials == nil {
		return nil
	}
	var out []StartPosition
	unnumbered := int32(0)
	for i := range s.Specials.Items {
		sp := &s.Specials.Items[i]
		what := values.Truncate(sp.SpecialWhat, 255)
		if !values.HasPrefixFold(what, StartPosPrefix) {
			continue
		}
		suffix := what[len(StartPosPrefix):]
		var number int32
		if suffix != "" && suffix[0] >= '0' && suffix[0] <= '9' {
			number = tdf.Atol(suffix)
		} else {
			unnumbered++
			number = unnumbered
		}
		slot := number
		if number > 0 {
			slot = number - 1
		}
		out = append(out, StartPosition{
			Number: int(number), Slot: int(slot),
			X: int(int16(sp.XPos)), Z: int(int16(sp.ZPos)),
			Special: sp,
		})
	}
	return out
}

// FeaturePlacement is a pre-placed feature the game puts on the map.
type FeaturePlacement struct {
	Name    string
	X, Z    int
	Special *Special
}

// GameFeatures returns the schema's pre-placed features the game places, in
// order: entries of [features] with both XPos and ZPos present and not
// negative. The game treats a missing XPos or ZPos as -1 and skips the entry,
// so a feature at column or row 0 needs an explicit 0.
func (s *Schema) GameFeatures() []FeaturePlacement {
	if s.Features == nil {
		return nil
	}
	var out []FeaturePlacement
	for i := range s.Features.Items {
		f := &s.Features.Items[i]
		x, z := -1, -1
		if values.Present(&f.Meta, "xpos", f.XPos != 0) {
			x = f.XPos
		}
		if values.Present(&f.Meta, "zpos", f.ZPos != 0) {
			z = f.ZPos
		}
		if x < 0 || z < 0 {
			continue
		}
		out = append(out, FeaturePlacement{Name: f.FeatureName, X: x, Z: z, Special: f})
	}
	return out
}

// EffectiveHealthPercentage returns a pre-placed unit's starting health in
// percent: HealthPercentage kept to 16 bits, or DefaultHealthPercentage when
// the key is missing (an explicit 0 stays 0).
func (sp *Special) EffectiveHealthPercentage() int {
	if values.Present(&sp.Meta, "healthpercentage", sp.HealthPercentage != 0) {
		return int(int16(sp.HealthPercentage))
	}
	return DefaultHealthPercentage
}

// EffectivePlayer returns the player a pre-placed unit belongs to: Player kept
// to 8 bits, with 0 (or a missing key) meaning player 1.
func (sp *Special) EffectivePlayer() int {
	p := int(uint8(sp.Player))
	if p == 0 {
		return 1
	}
	return p
}

// InitialGroupNumber returns the group a pre-placed unit starts in, as the
// game reads it: InitialGroup's leading number (Atol) kept to its low 4 bits,
// so "patrol" is 0 and "17" is 1.
func (sp *Special) InitialGroupNumber() int {
	return int(uint8(tdf.Atol(sp.InitialGroup)) & 0x0f)
}

// BuildPriorityNumber returns BuildPriority as the game reads it: its leading
// number (Atol) kept to 16 bits, signed, so "CORHRK" is 0.
func (sp *Special) BuildPriorityNumber() int {
	return int(int16(tdf.Atol(sp.BuildPriority)))
}

// Add appends e to the specials, naming it special<N> when its Key is empty
// (N the lowest number no entry is named with, as Marshal would name it), and
// returns a pointer to the stored entry.
func (s *Specials) Add(e Special) *Special {
	return addItem(&s.Items, "special", e)
}

// Add appends e to the units, naming it unit<N> when its Key is empty (see
// Specials.Add).
func (u *Units) Add(e Special) *Special {
	return addItem(&u.Items, "unit", e)
}

// Add appends e to the features, naming it feature<N> when its Key is empty
// (see Specials.Add).
func (f *Features) Add(e Special) *Special {
	return addItem(&f.Items, "feature", e)
}

func addItem(items *[]Special, stem string, e Special) *Special {
	*items = append(*items, e)
	names := make([]string, len(*items))
	for i := range *items {
		names[i] = (*items)[i].Key
	}
	names = tdf.ElementNames(stem, names)
	last := &(*items)[len(*items)-1]
	last.Key = names[len(names)-1]
	return last
}

// Check reports what the game ignores or reads differently in the map: no
// [GlobalHeader], a second one, sections of a TA: Kingdoms map, schemas the
// game never reads, no schema a skirmish or multiplayer game can use, second
// [specials], [units] or [features] sections, start positions sharing a slot or
// outside 16 bits, and features the game skips for a missing position.
func (m *Map) Check() []common.Warning {
	var out []common.Warning
	warn := func(section, key, format string, args ...any) {
		out = append(out, common.Warning{Section: section, Key: key, Message: fmt.Sprintf(format, args...)})
	}
	if !m.Meta.HasSection("GlobalHeader") && (len(m.Sections) > 0 || len(m.Remaining) > 0) {
		warn("", "", "no [GlobalHeader] section; the game refuses the map")
	}
	for _, s := range m.Sections {
		if values.EqualFold(s.Key, "GlobalHeader") {
			warn(s.Key, "", "a second [GlobalHeader]; the game reads the first")
		}
	}
	h := &m.Header
	hdr := "GlobalHeader"
	if h.Key != "" {
		hdr = h.Key
	}
	for _, s := range h.Sections {
		if values.EqualFold(s.Key, "Map Data") {
			warn(hdr+"/"+s.Key, "", "a TA: Kingdoms section; Total Annihilation never reads it")
		}
	}
	for _, name := range h.UnreachableSchemas() {
		warn(hdr+"/"+name, "", "the game reads only Schema 0, Schema 1, ... up to the first missing number")
	}
	if h.MultiplayerSchema(0) == nil {
		warn(hdr, "", "no Network 1 to Network 4 schema with start positions; skirmish and multiplayer games cannot use the map")
	}
	for _, s := range h.GameSchemas() {
		path := hdr + "/" + s.Key
		for _, c := range s.Sections {
			for _, name := range []string{"specials", "units", "features"} {
				if values.EqualFold(c.Key, name) {
					warn(path+"/"+c.Key, "", "a second [%s]; the game reads the first", name)
				}
			}
		}
		slots := map[int]string{}
		for _, p := range s.StartPositions() {
			if prev, ok := slots[p.Slot]; ok {
				warn(path+"/specials/"+p.Special.Key, "specialwhat",
					"%s is player slot %d, like %s", p.Special.SpecialWhat, p.Slot, prev)
			} else {
				slots[p.Slot] = p.Special.SpecialWhat
			}
			for _, c := range []struct {
				key string
				v   int
			}{{"XPos", p.Special.XPos}, {"ZPos", p.Special.ZPos}} {
				if c.v != int(int16(c.v)) {
					warn(path+"/specials/"+p.Special.Key, c.key,
						"%d is outside 16 bits; the game uses %d", c.v, int16(c.v))
				}
			}
		}
		if s.Features != nil {
			placed := map[*Special]bool{}
			for _, f := range s.GameFeatures() {
				placed[f.Special] = true
			}
			for i := range s.Features.Items {
				f := &s.Features.Items[i]
				if !placed[f] {
					warn(path+"/features/"+f.Key, "", "no XPos or ZPos, or a negative one; the game skips the feature")
				}
			}
		}
	}
	return out
}
