package ta

import (
	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// UnitInfo is the [UNITINFO] section of a unit's .fbi file. Decode a single
// file with
//
//	var u ta.Unit
//	err := tdf.Unmarshal(data, &u)
//
// where Unit wraps the single [UNITINFO] section. Fields shared with
// TA:Kingdoms live on the embedded common.UnitInfoBase; the fields below are
// unique to Total Annihilation.
//
// The fields hold the values as written. The Effective accessors (such as
// EffectiveStandingMoveOrder and Movement) return what TA 3.1c uses: its
// default for a key the file leaves out, its number reading (integers for
// costs, fractions for storage) and the width it stores each value in.
type UnitInfo struct {
	common.UnitInfoBase

	Designation string `tdf:"designation,omitempty"`

	BuildCostEnergy int     `tdf:"buildcostenergy,omitempty"`
	BuildCostMetal  float64 `tdf:"buildcostmetal,omitempty"`
	BuildTime       int     `tdf:"buildtime,omitempty"`
	// BuildCost is TA:Kingdoms' single-resource (mana) price; the mana
	// economy fields below are likewise TA:K-only. They live here because
	// this schema doubles as the studio's universal FBI parse target; a TA
	// file simply never sets them.
	BuildCost        int     `tdf:"buildcost,omitempty"`
	ManaRechargeRate float64 `tdf:"manarechargerate,omitempty"`
	MaxMana          int     `tdf:"maxmana,omitempty"`
	MogriumIncome    float64 `tdf:"mogriumincome,omitempty"`
	MogriumStorage   int     `tdf:"mogriumstorage,omitempty"`

	DamageModifier float64 `tdf:"damagemodifier,omitempty"`
	MinWaterDepth  int     `tdf:"minwaterdepth,omitempty"`

	// Transports: capacity is the total size budget, size the largest single
	// unit accepted, TransMaxUnits a hard unit-count cap (the Atlas's 1).
	TransportCapacity int `tdf:"transportcapacity,omitempty"`
	TransportSize     int `tdf:"transportsize,omitempty"`
	TransMaxUnits     int `tdf:"transmaxunits,omitempty"`

	EnergyUse     float64 `tdf:"energyuse,omitempty"`
	EnergyMake    float64 `tdf:"energymake,omitempty"`
	MetalMake     float64 `tdf:"metalmake,omitempty"`
	MakesMetal    float64 `tdf:"makesmetal,omitempty"`
	ExtractsMetal float64 `tdf:"extractsmetal,omitempty"`
	EnergyStorage int     `tdf:"energystorage,omitempty"`
	MetalStorage  int     `tdf:"metalstorage,omitempty"`

	RadarDistanceJam int `tdf:"radardistancejam,omitempty"`
	SonarDistanceJam int `tdf:"sonardistancejam,omitempty"`

	ThreeD     int `tdf:"threed,omitempty"`
	ZBuffer    int `tdf:"zbuffer,omitempty"`
	NoAutoFire int `tdf:"noautofire,omitempty"`

	ExplodeAs      string `tdf:"explodeas,omitempty"`
	SelfDestructAs string `tdf:"selfdestructas,omitempty"`

	// Movement.
	SteeringMode    int     `tdf:"steeringmode,omitempty"`
	Scale           float64 `tdf:"scale,omitempty"`
	AltFromSeaLevel float64 `tdf:"altfromsealevel,omitempty"`
	Amphibious      int     `tdf:"amphibious,omitempty"`

	HoverAttack int `tdf:"hoverattack,omitempty"`
	OnOffable   int `tdf:"onoffable,omitempty"`

	// IsAirBase marks an air repair pad: aircraft land on it to rearm and
	// repair, and it holds them while they service.
	IsAirBase int `tdf:"isairbase,omitempty"`

	// Orders / behaviour flags.
	CanReclamate      int    `tdf:"canreclamate,omitempty"`
	CanCapture        int    `tdf:"cancapture,omitempty"`
	CanResurrect      int    `tdf:"canresurrect,omitempty"`
	NoChaseCategory   string `tdf:"nochasecategory,omitempty"`
	StandingMoveOrder int    `tdf:"standingmoveorder,omitempty"`
	StandingFireOrder int    `tdf:"standingfireorder,omitempty"`
	FireStandOrders   int    `tdf:"firestandorders,omitempty"`
	MobileStandOrders int    `tdf:"mobilestandorders,omitempty"`
	OvrAdjust         int    `tdf:"ovradjust,omitempty"`

	// Weapons.
	Weapon1               string `tdf:"weapon1,omitempty"`
	Weapon2               string `tdf:"weapon2,omitempty"`
	Weapon3               string `tdf:"weapon3,omitempty"`
	BadTargetCategory     string `tdf:"badtargetcategory,omitempty"`
	WpriBadTargetCategory string `tdf:"wpri_badtargetcategory,omitempty"`
	WsecBadTargetCategory string `tdf:"wsec_badtargetcategory,omitempty"`

	// Localised display strings.
	GermanName          string `tdf:"germanname,omitempty"`
	GermanDescription   string `tdf:"germandescription,omitempty"`
	FrenchName          string `tdf:"frenchname,omitempty"`
	FrenchDescription   string `tdf:"frenchdescription,omitempty"`
	SpanishName         string `tdf:"spanishname,omitempty"`
	SpanishDescription  string `tdf:"spanishdescription,omitempty"`
	ItalianName         string `tdf:"italianname,omitempty"`
	ItalianDescription  string `tdf:"italiandescription,omitempty"`
	JapaneseName        string `tdf:"japanesename,omitempty"`
	PigLatinName        string `tdf:"piglatinname,omitempty"`
	PigLatinDescription string `tdf:"piglatindescription,omitempty"`
}

// UnitInfo satisfies the shared common.UnitInfo interface via its embedded base.
var _ common.UnitInfo = (*UnitInfo)(nil)

// Unit wraps a unit .fbi file, which contains a single [UNITINFO] section.
//
//	var u ta.Unit
//	err := tdf.Unmarshal(data, &u)
//
// The game reads the first [UNITINFO] of the file; a second one, and any other
// top-level section, is kept in Sections so the file round-trips.
type Unit struct {
	Info UnitInfo `tdf:"UNITINFO"`

	// Sections preserves every other top-level section, including a second
	// [UNITINFO], in order.
	Sections []common.Section `tdf:",sections"`

	// Remaining preserves any key=value outside the sections.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's section order and spelling (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}
