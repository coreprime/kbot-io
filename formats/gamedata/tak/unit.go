// Package tak provides typed Go representations of Total Annihilation: Kingdoms
// game-data files, built on the github.com/coreprime/kbot-io/formats/tdf codec.
//
// TA:Kingdoms uses the same TDF text grammar as Total Annihilation but a
// different schema: a unit .fbi file is a document of sibling top-level
// sections ([UNITINFO], [WEAPON1..3], [EXPLODEAS]) rather than a single
// [UNITINFO]. Well-known keys are exposed as named fields; any remaining keys
// are preserved in a Remaining map and unmodelled sections in a Sections slice.
// Fields shared with Total Annihilation are factored into embedded base types
// from the common package.
//
// Every type carries a tdf.Meta, which records which keys and sections the
// source had, their order and spelling, and each value's text, so Marshal
// writes back explicit zeros and unchanged text and keeps the source's order.
// Meta.Present tells a missing key from an explicit zero. The defaults TA:
// Kingdoms applies to missing keys are not established, so this package, unlike
// the ta package, has no Effective accessors; the fields hold the values as
// written.
//
// # Reading TA: Kingdoms text
//
// The tdf package reads every file with the TA 3.1c grammar by default, and
// that includes TA: Kingdoms data; whether TA: Kingdoms' own reader follows
// the same rules in every case is not established. Three retail TA: Kingdoms
// files read differently under that grammar than under the line-based reader
// of earlier versions of this module, because stray text joins the key that
// follows it:
//
//   - features/zhon/zonruin.tdf: a line holding only ';' joins the next key,
//     so [ZonRuin12] loses its damage value;
//   - translate/messages.tdf: a ';' inside the French text of
//     [DO_YOU_WANT_TO_WATCH_GAME] ends that value, and the rest joins the
//     Italian key, which is lost;
//   - translate/customkeys.tdf: "English = ;;" in [SYMBOL_3B] swallows the
//     [SYMBOL_3C] section that follows.
//
// Callers who want the earlier handling, which drops such stray text, can read
// with tdf.UnmarshalWith(data, &v, tdf.ParseOptions{SkipStrayText: true}) or
// tdf.ParseWith. ParseOptions.Strict applies the TA 3.1c rules for refusing a
// file, so it refuses those files and TA: Kingdoms' .gui files, which are not
// TDF text at all (they are lists of numbers and names; the default reader
// finds no fields in them).
package tak

import (
	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// UnitInfo is the [UNITINFO] section of a TA:Kingdoms unit .fbi file. Fields
// shared with Total Annihilation live on the embedded common.UnitInfoBase; the
// fields below are unique to TA:Kingdoms.
type UnitInfo struct {
	common.UnitInfoBase

	BodyType string `tdf:"bodytype,omitempty"`
	SubType  string `tdf:"subtype,omitempty"`
	Model    string `tdf:"model,omitempty"`

	BuildCost        int     `tdf:"buildcost,omitempty"`
	BuildTime        float64 `tdf:"buildtime,omitempty"`
	ExperiencePoints int     `tdf:"experiencepoints,omitempty"`

	HealTime       float64 `tdf:"healtime,omitempty"`
	DamageCategory string  `tdf:"damagecategory,omitempty"`

	MaxMana          int     `tdf:"maxmana,omitempty"`
	ManaRechargeRate float64 `tdf:"manarechargerate,omitempty"`
	MogriumStorage   int     `tdf:"mogriumstorage,omitempty"`
	MogriumIncome    float64 `tdf:"mogriumincome,omitempty"`

	SoundClass string `tdf:"soundclass,omitempty"`
	ShadowGAF  string `tdf:"shadowgaf,omitempty"`

	// Movement.
	TurnInPlaceRate int     `tdf:"turninplacerate,omitempty"`
	WaterMultiplier float64 `tdf:"watermultiplier,omitempty"`
	RoadMultiplier  float64 `tdf:"roadmultiplier,omitempty"`

	// Orders / behaviour flags.
	CanReclaim        int `tdf:"canreclaim,omitempty"`
	StandingUnitOrder int `tdf:"standingunitorder,omitempty"`
	UnitStandOrders   int `tdf:"unitstandorders,omitempty"`

	Type string `tdf:"type,omitempty"`
	Wind int    `tdf:"wind,omitempty"`

	// Blood colours are space-separated "R G B" triples, modelled as RGBString.
	BloodColor1 *common.RGBString `tdf:"bloodcolor1,omitempty"`
	BloodColor2 *common.RGBString `tdf:"bloodcolor2,omitempty"`
	BloodColor3 *common.RGBString `tdf:"bloodcolor3,omitempty"`

	ButtonImageUp       string `tdf:"buttonimageup,omitempty"`
	ButtonImageDown     string `tdf:"buttonimagedown,omitempty"`
	ButtonImageSelected string `tdf:"buttonimageselected,omitempty"`
	ButtonImageDisabled string `tdf:"buttonimagedisabled,omitempty"`

	// Area "adjust" abilities are optional nested sections of [UNITINFO].
	AdjustJoy    *Adjust `tdf:"adjustjoy,omitempty"`
	AdjustArmor  *Adjust `tdf:"adjustarmor,omitempty"`
	AdjustAttack *Adjust `tdf:"adjustattack,omitempty"`
}

// UnitInfo satisfies the shared common.UnitInfo interface via its embedded base.
var _ common.UnitInfo = (*UnitInfo)(nil)

// Adjust is an area-effect ability subsection of [UNITINFO]
// ([AdjustJoy], [AdjustArmor], [AdjustAttack]).
type Adjust struct {
	Adjustment        float64 `tdf:"adjustment,omitempty"`
	AffectsEnemy      int     `tdf:"affectsenemy,omitempty"`
	EdgeEffectiveness float64 `tdf:"edgeeffectiveness,omitempty"`
	Radius            int     `tdf:"radius,omitempty"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves any section nested in the ability.
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Weapon is a [WEAPONn] section of a TA:Kingdoms unit .fbi file. Fields shared
// with Total Annihilation live on the embedded common.WeaponBase; the fields
// below are unique to TA:Kingdoms.
type Weapon struct {
	common.WeaponBase

	Type     string `tdf:"type,omitempty"`
	SubType  string `tdf:"subtype,omitempty"`
	MinRange int    `tdf:"minrange,omitempty"`

	AimTolerance int     `tdf:"aimtolerance,omitempty"`
	ManaPerShot  float64 `tdf:"manapershot,omitempty"`

	DamageType          string `tdf:"damagetype,omitempty"`
	ExplosionClass      string `tdf:"explosionclass,omitempty"`
	WaterExplosionClass string `tdf:"waterexplosionclass,omitempty"`

	// Projectile presentation flags. Nimbus marks the glow halo the engine
	// draws around magic projectiles; LightMap ("small"/"medium"/"large")
	// sizes the light splash the shot casts on the ground; HwEffect names a
	// hardware-rendered stream effect ("fire", "lightning", ...).
	Nimbus   int    `tdf:"nimbus,omitempty"`
	LightMap string `tdf:"lightmap,omitempty"`
	HwEffect string `tdf:"hweffect,omitempty"`

	// Tracer/beam colours, stored as space-separated "R G B" triples.
	InnerColor  *common.RGBString `tdf:"innercolor,omitempty"`
	MiddleColor *common.RGBString `tdf:"middlecolor,omitempty"`
	OuterColor  *common.RGBString `tdf:"outercolor,omitempty"`

	WeaponArt     string `tdf:"weaponart,omitempty"`
	ShadowArt     string `tdf:"shadowart,omitempty"`
	ShadowGAF     string `tdf:"shadowgaf,omitempty"`
	SoundHitClass string `tdf:"soundhitclass,omitempty"`

	// Per-target-category damage multipliers: [DAMAGE]{ default=1; fort=0.2; }.
	Damage map[string]float64 `tdf:"damage,omitempty"`
}

// Weapon satisfies the shared common.Weapon interface via its embedded base.
var _ common.Weapon = (*Weapon)(nil)

// ExplodeAs is the [EXPLODEAS] section of a TA:Kingdoms unit .fbi file: the
// effect produced when the unit is destroyed.
type ExplodeAs struct {
	Key string `tdf:",name"` // section header, always EXPLODEAS

	Name                string  `tdf:"name,omitempty"`
	AreaOfEffect        int     `tdf:"areaofeffect,omitempty"`
	DamageType          string  `tdf:"damagetype,omitempty"`
	EdgeEffectiveness   float64 `tdf:"edgeeffectiveness,omitempty"`
	ExplosionClass      string  `tdf:"explosionclass,omitempty"`
	WaterExplosionClass string  `tdf:"waterexplosionclass,omitempty"`

	Damage map[string]float64 `tdf:"damage,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves any other section nested in [EXPLODEAS].
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Unit wraps a TA:Kingdoms unit .fbi file: one [UNITINFO] plus up to three
// [WEAPONn] sections and an optional [EXPLODEAS], as sibling top-level blocks.
//
//	var u tak.Unit
//	err := tdf.Unmarshal(data, &u)
type Unit struct {
	Info      UnitInfo   `tdf:"UNITINFO"`
	Weapon1   *Weapon    `tdf:"WEAPON1,omitempty"`
	Weapon2   *Weapon    `tdf:"WEAPON2,omitempty"`
	Weapon3   *Weapon    `tdf:"WEAPON3,omitempty"`
	ExplodeAs *ExplodeAs `tdf:"EXPLODEAS,omitempty"`

	// Sections preserves every other top-level section, including a second
	// section of one of the names above, in order.
	Sections []common.Section `tdf:",sections"`

	// Remaining preserves any key=value outside the sections.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's section order and spelling (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}
