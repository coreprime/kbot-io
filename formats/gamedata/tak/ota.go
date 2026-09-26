package tak

import (
	"errors"
	"fmt"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// Map wraps a TA:Kingdoms mission/map .ota file, whose sole top-level section
// is [GlobalHeader]. Decode a file with
//
//	var m tak.Map
//	err := tdf.Unmarshal(data, &m)
//
// or with ReadMap, which also refuses text with no [GlobalHeader]. The structs
// keep everything the file holds: keys and sections they do not model sit in
// Remaining and Sections, and each struct's Meta keeps the source's key
// presence, order, spelling and value text, so decoding and re-marshalling
// changes nothing another reader sees. Check reports sections of a Total
// Annihilation map, such as [Schema 0], which a TA: Kingdoms map does not use.
type Map struct {
	Header GlobalHeader `tdf:"GlobalHeader"`

	// Sections preserves every other top-level section, including a second
	// [GlobalHeader], in order.
	Sections []common.Section `tdf:",sections"`

	// Remaining preserves any key=value outside the sections.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's section order and the header's spelling
	// (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// GlobalHeader is the [GlobalHeader] section of a TA:Kingdoms .ota file. Unlike
// Total Annihilation it carries a single [Map Data] block rather than a counted
// list of [Schema N] blocks. Fields shared with Total Annihilation live on the
// embedded common.GlobalHeaderBase; the fields below are unique to TA:Kingdoms.
type GlobalHeader struct {
	common.GlobalHeaderBase

	Kingdom   string `tdf:"kingdom,omitempty"`
	Copyright string `tdf:"copyright,omitempty"`
	IsMission int    `tdf:"ismission,omitempty"`

	MapData *MapData `tdf:"Map Data,omitempty"`
}

// GlobalHeader satisfies the shared common.GlobalHeader interface via its
// embedded base.
var _ common.GlobalHeader = (*GlobalHeader)(nil)

// MapData is the [Map Data] subsection of a TA:Kingdoms .ota file.
type MapData struct {
	Key string `tdf:",name"` // section header, always Map Data

	Type      string `tdf:"type,omitempty"`
	AIProfile string `tdf:"aiprofile,omitempty"`

	Specials *Specials `tdf:"specials,omitempty"`
	Units    *Units    `tdf:"units,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves every other child section, including a second
	// [specials] or [units], in order.
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Specials is the [specials] subsection: start positions and scripted markers
// ([special0], [special1], ...).
type Specials struct {
	// Items holds every child section in order, whatever its name.
	Items []Placement `tdf:",sections"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's key and section order (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Units is the [units] subsection: pre-placed units ([unit0], [unit1], ...).
type Units struct {
	// Items holds every child section in order, whatever its name.
	Items []Placement `tdf:",sections"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's key and section order (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Placement is one [specialN] or [unitN] entry on a TA:Kingdoms map. A new
// entry with an empty Key is written as an unnamed section; Specials.Add and
// Units.Add name it.
type Placement struct {
	Key string `tdf:",name"` // section header, e.g. "unit0"

	SpecialWhat string `tdf:"specialwhat,omitempty"`
	UnitName    string `tdf:"unitname,omitempty"`

	XPos int `tdf:"xpos,omitempty"`
	YPos int `tdf:"ypos,omitempty"`
	ZPos int `tdf:"zpos,omitempty"`

	Player           int `tdf:"player,omitempty"`
	Kills            int `tdf:"kills,omitempty"`
	HealthPercentage int `tdf:"healthpercentage,omitempty"`
	ManaPercentage   int `tdf:"manapercentage,omitempty"`
	Angle            int `tdf:"angle,omitempty"`

	// Ident is a string because maps label placements with non-numeric ids
	// (e.g. "ABLE") as well as plain numbers.
	Ident string `tdf:"ident,omitempty"`

	InitialMission string `tdf:"initialmission,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves any section nested in the entry.
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Add appends p to the specials, naming it special<N> (N its index) when its
// Key is empty, and returns a pointer to the stored entry.
func (s *Specials) Add(p Placement) *Placement { return addPlacement(&s.Items, "special", p) }

// Add appends p to the units, naming it unit<N> when its Key is empty.
func (u *Units) Add(p Placement) *Placement { return addPlacement(&u.Items, "unit", p) }

func addPlacement(items *[]Placement, stem string, p Placement) *Placement {
	if p.Key == "" {
		p.Key = fmt.Sprintf("%s%d", stem, len(*items))
	}
	*items = append(*items, p)
	return &(*items)[len(*items)-1]
}

// ErrNoGlobalHeader is returned by ReadMap for text with no [GlobalHeader]
// section.
var ErrNoGlobalHeader = errors.New("tak: no [GlobalHeader] section")

// ReadMap decodes a TA: Kingdoms .ota file. It fails for text with no
// [GlobalHeader] section, which tdf.Unmarshal accepts as an empty map.
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

// Check reports what looks wrong for a TA: Kingdoms map: no [GlobalHeader], a
// second one, a missing [Map Data] and Total Annihilation [Schema N] sections,
// which suggest the file is a Total Annihilation map.
func (m *Map) Check() []common.Warning {
	var out []common.Warning
	if !m.Meta.HasSection("GlobalHeader") && (len(m.Sections) > 0 || len(m.Remaining) > 0) {
		out = append(out, common.Warning{Message: "no [GlobalHeader] section"})
	}
	for _, s := range m.Sections {
		if values.EqualFold(s.Key, "GlobalHeader") {
			out = append(out, common.Warning{Section: s.Key, Message: "a second [GlobalHeader]; readers use the first"})
		}
	}
	h := &m.Header
	schemas := 0
	for _, s := range h.Sections {
		if values.HasPrefixFold(s.Key, "schema") {
			schemas++
		}
	}
	if schemas > 0 {
		out = append(out, common.Warning{Section: "GlobalHeader", Message: fmt.Sprintf(
			"%d [Schema] section(s), which only Total Annihilation maps have; read the file with the ta package", schemas)})
	}
	if h.MapData == nil && m.Meta.HasSection("GlobalHeader") {
		out = append(out, common.Warning{Section: "GlobalHeader", Message: "no [Map Data] section"})
	}
	return out
}
