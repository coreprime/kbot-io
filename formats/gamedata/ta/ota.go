package ta

import (
	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// Map wraps a mission/map .ota file, whose sole top-level section is
// [GlobalHeader]. Decode a file with
//
//	var m ta.Map
//	err := tdf.Unmarshal(data, &m)
//
// or with ReadMap, which also refuses text with no [GlobalHeader].
//
// The structs keep everything the file holds: keys and sections they do not
// model sit in Remaining and Sections, and each struct's Meta keeps the
// source's key presence, order, spelling and value text. Decoding and
// re-marshalling an OTA therefore changes nothing the game reads, and an edit
// changes only the values it touches.
//
// The methods give the game's view of the file: which schemas it can find
// (Schema, GameSchemas), which one a game uses (MultiplayerSchema,
// CampaignSchema), how it numbers start positions (Schema.StartPositions) and
// the values it uses for missing or fractional keys (the Effective
// accessors). Check reports what the game ignores or reads differently.
type Map struct {
	Header GlobalHeader `tdf:"GlobalHeader"`

	// Sections preserves every other top-level section, including a second
	// [GlobalHeader], which the game ignores.
	Sections []common.Section `tdf:",sections"`

	// Remaining preserves any key=value outside the sections.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's section order and the header's spelling
	// (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// GlobalHeader is the [GlobalHeader] section of an .ota file: map metadata plus
// one or more start-position/resource [Schema N] blocks. Fields shared with
// TA:Kingdoms live on the embedded common.GlobalHeaderBase; the fields below
// are unique to Total Annihilation.
type GlobalHeader struct {
	common.GlobalHeaderBase

	MissionHint  string `tdf:"missionhint,omitempty"`
	Planet       string `tdf:"planet,omitempty"`
	Brief        string `tdf:"brief,omitempty"`
	Narration    string `tdf:"narration,omitempty"`
	Glamour      string `tdf:"glamour,omitempty"`
	GlamourSound string `tdf:"glamoursound,omitempty"`

	SeaLevel        int `tdf:"sealevel,omitempty"`
	ImpassibleWater int `tdf:"impassiblewater,omitempty"`

	// Schemas holds every child section whose name starts with "Schema "
	// (ignoring case), in file order. The game finds only Schema 0, Schema 1,
	// ... up to the first missing number (see GameSchemas); other sections,
	// such as [Schema0] without the space, are kept in Sections.
	//
	// A schema with an empty Key is written as [Schema N], N its index in
	// Schemas; AddSchema names it after the highest number in use. The game
	// ignores the SCHEMACOUNT key, which is kept in Remaining as written.
	Schemas []Schema `tdf:"Schema "`
}

// GlobalHeader satisfies the shared common.GlobalHeader interface via its
// embedded base.
var _ common.GlobalHeader = (*GlobalHeader)(nil)

// Schema is one [Schema N] block of an .ota file: a per-difficulty resource and
// start-position configuration.
type Schema struct {
	Key string `tdf:",name"` // section header, e.g. "Schema 0"

	// Type selects the games the schema serves: Easy, Medium or Hard for a
	// campaign mission, Network 1 to Network 4 for skirmish and multiplayer.
	Type      string `tdf:"type,omitempty"`
	AIProfile string `tdf:"aiprofile,omitempty"`

	SurfaceMetal   int `tdf:"surfacemetal,omitempty"`
	MohoMetal      int `tdf:"mohometal,omitempty"`
	HumanMetal     int `tdf:"humanmetal,omitempty"`
	ComputerMetal  int `tdf:"computermetal,omitempty"`
	HumanEnergy    int `tdf:"humanenergy,omitempty"`
	ComputerEnergy int `tdf:"computerenergy,omitempty"`

	// The game reads MeteorDensity, MeteorDuration and MeteorInterval as
	// fractions; MeteorDuration and MeteorInterval hold only the whole part
	// (see EffectiveMeteorDuration and Meteors).
	MeteorWeapon   string  `tdf:"meteorweapon,omitempty"`
	MeteorRadius   int     `tdf:"meteorradius,omitempty"`
	MeteorDensity  float64 `tdf:"meteordensity,omitempty"`
	MeteorDuration int     `tdf:"meteorduration,omitempty"`
	MeteorInterval int     `tdf:"meteorinterval,omitempty"`

	// Specials, Units and Features are the schema's first [specials],
	// [units] and [features] sections, the ones the game reads.
	Specials *Specials `tdf:"specials,omitempty"`
	Units    *Units    `tdf:"units,omitempty"`
	Features *Features `tdf:"features,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves every other child section, including a second
	// [specials], [units] or [features], in order.
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so explicit zeros and fractions survive a round trip.
	Meta tdf.Meta `tdf:",meta"`
}

// Specials is the [specials] subsection of a schema: start positions and
// other placed entries ([special0], [special1], ...).
type Specials struct {
	// Items holds every child section in order, whatever its name: the game
	// reads the children by position. An entry with an empty Key is written
	// as [special<N>], N the lowest number no other entry is named with.
	Items []Special `tdf:"special,sections"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's key and section order (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Units is the [units] subsection of a schema: pre-placed units ([unit0],
// [unit1], ...). Entries share the placement fields of Special.
type Units struct {
	// Items holds every child section in order, whatever its name: the game
	// reads the children by position. An entry with an empty Key is written
	// as [unit<N>], N the lowest number no other entry is named with.
	Items []Special `tdf:"unit,sections"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's key and section order (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Features is the [features] subsection of a schema: pre-placed features
// ([feature0], [feature1], ...). Entries share the placement fields of Special.
type Features struct {
	// Items holds every child section in order, whatever its name: the game
	// reads the children by position. An entry with an empty Key is written
	// as [feature<N>], N the lowest number no other entry is named with.
	Items []Special `tdf:"feature,sections"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's key and section order (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Special is one [specialN] entry: a unit, feature or start position placed on
// the map. The game goes by the entries' positions, not their names. A new
// entry with an empty Key is written as [special<N>], [unit<N>] or
// [feature<N>], N the lowest number no other entry of its list is named with;
// Specials.Add, Units.Add and Features.Add give it that name when adding it.
type Special struct {
	Key string `tdf:",name"` // section header, e.g. "special0"

	SpecialWhat string `tdf:"specialwhat,omitempty"`
	UnitName    string `tdf:"unitname,omitempty"`
	FeatureName string `tdf:"featurename,omitempty"`

	XPos int `tdf:"xpos,omitempty"`
	YPos int `tdf:"ypos,omitempty"`
	ZPos int `tdf:"zpos,omitempty"`

	Player           int `tdf:"player,omitempty"`
	Kills            int `tdf:"kills,omitempty"`
	HealthPercentage int `tdf:"healthpercentage,omitempty"`
	Angle            int `tdf:"angle,omitempty"`

	// Ident, InitialGroup and BuildPriority keep their text, since maps
	// write labels for them (ident "AIRHEAD", group "patrol", buildpriority
	// "CORHRK") as well as numbers. The game reads Ident as text but
	// InitialGroup and BuildPriority as numbers (Atol), so a label reads as
	// 0: see InitialGroupNumber and BuildPriorityNumber.
	Ident         string `tdf:"ident,omitempty"`
	InitialGroup  string `tdf:"initialgroup,omitempty"`
	BuildPriority string `tdf:"buildpriority,omitempty"`

	InitialMission    string `tdf:"initialmission,omitempty"`
	CreationCountdown int    `tdf:"creationcountdown,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves any section nested in the entry.
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit 0 (such as a feature at column 0, or 0% health)
	// survives a round trip.
	Meta tdf.Meta `tdf:",meta"`
}
