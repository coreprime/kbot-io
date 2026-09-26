// Package ta provides typed Go representations of Total Annihilation game-data
// files (TDF/FBI/GUI/OTA), built on the github.com/coreprime/kbot-io/formats/tdf
// codec.
//
// Each type maps one TDF schema. Well-known keys are exposed as named fields;
// any remaining keys are preserved in a Remaining map and unmodelled sections
// in a Sections slice. Fields shared with TA:Kingdoms are factored into
// embedded base types from the common package. Use tdf.Unmarshal/tdf.Marshal
// with these types.
//
// # Round trips
//
// Every type carries a tdf.Meta, which records which keys and sections the
// source had, their order and spelling, and each value's text. Marshal then
// writes every key the source had, including explicit zeros, keeps the text of
// unchanged values ("13O", "0.5", "1e3") and the source's order, so decoding
// and re-marshalling a file changes nothing TA 3.1c reads. A second section
// of a name the game looks up once (a second [UNITINFO], [CANBUILD] or
// [specials]) is kept in Sections; the typed field holds the first, which is
// the one the game reads.
//
// # Values the game uses
//
// The typed fields hold the values as written, read with the game's number
// rules (tdf.Atol, tdf.Atof). TA 3.1c then applies defaults to keys a file
// leaves out, which are often not zero (a missing StandingMoveOrder is 2, a
// missing weapon range is 32767, a missing MaxWaterDepth is 10000), keeps
// values to fixed widths (WaterLine to 8 bits, standing orders to 2) and reads
// some keys as fractions that an int field cannot hold (weapon metalpershot,
// OTA tidalstrength). The Effective accessors return what the game uses, and
// Set accessors write fractions exactly. Resolvers follow the game's lookups:
// UnitInfo.Movement for movement classes, WeaponTable for weapon IDs and
// names, GameSides, CanBuildBuilder.BuildList and SoundClass.Sounds for the
// numbered lists, GlobalHeader.GameSchemas and MultiplayerSchema for OTA
// schemas. Constants named ...Max give the longest text the game keeps for a
// key.
//
// # Checks
//
// Check methods and Check functions (CheckWeapons, CheckSides, CheckMoveInfo,
// CheckFeatures) report what the game ignores or reads differently from how a
// file looks: text longer than the game keeps, values outside the bits it
// keeps, sections it never reaches. Reading with
// tdf.UnmarshalWith(data, &v, tdf.ParseOptions{Strict: true}) refuses text
// the game refuses (a missing ';' at the end of the file, an unterminated
// section) instead of repairing it.
package ta

import "github.com/coreprime/kbot-io/formats/gamedata/common"

// Weapon is one section of a weapons/*.tdf file. Each weapon is a top-level
// [SECTION]; decode a file with
//
//	var weapons []ta.Weapon
//	err := tdf.Unmarshal(data, &weapons)
//
// TA 3.1c loads weapons only from the .tdf files directly in the weapons
// directory (see IsWeaponFile); gamedata/weapons.tdf, which retail data also
// ships, is never read. WeaponTable builds the game's table from those files.
//
// Fields shared with TA:Kingdoms live on the embedded common.WeaponBase; the
// fields below are unique to Total Annihilation. The Effective accessors
// return what the game uses where that differs from the field: its default for
// a missing key (range 32767, minbarrelangle -11.25) and fractions the int
// fields cannot hold (turnrate, metalpershot, holdtime).
type Weapon struct {
	common.WeaponBase

	// ID is the weapon's slot in the game's table, 0..255. A section
	// without an ID key reads as 0 here and is written back without one;
	// use SetID (or Meta.Mark("id")) to write ID 0 for a new weapon. See
	// EffectiveID.
	ID         int `tdf:"id,omitempty"`
	RenderType int `tdf:"rendertype,omitempty"`

	// Basic category flags.
	Ballistic   int `tdf:"ballistic,omitempty"`
	LineOfSight int `tdf:"lineofsight,omitempty"`
	Dropped     int `tdf:"dropped,omitempty"`
	Turret      int `tdf:"turret,omitempty"`
	NoExplode   int `tdf:"noexplode,omitempty"`

	// Trajectory / targeting category flags.
	SelfProp     int `tdf:"selfprop,omitempty"`
	Tracks       int `tdf:"tracks,omitempty"`
	CommandFire  int `tdf:"commandfire,omitempty"`
	VLaunch      int `tdf:"vlaunch,omitempty"`
	Cruise       int `tdf:"cruise,omitempty"`
	Guidance     int `tdf:"guidance,omitempty"`
	WaterWeapon  int `tdf:"waterweapon,omitempty"`
	TwoPhase     int `tdf:"twophase,omitempty"`
	NoAutoRange  int `tdf:"noautorange,omitempty"`
	BurnBlow     int `tdf:"burnblow,omitempty"`
	UnitsOnly    int `tdf:"unitsonly,omitempty"`
	Targetable   int `tdf:"targetable,omitempty"`
	Interceptor  int `tdf:"interceptor,omitempty"`
	Meteor       int `tdf:"meteor,omitempty"`
	Paralyzer    int `tdf:"paralyzer,omitempty"`
	NoRadar      int `tdf:"noradar,omitempty"`
	GroundBounce int `tdf:"groundbounce,omitempty"`
	Stockpile    int `tdf:"stockpile,omitempty"`
	ToAirWeapon  int `tdf:"toairweapon,omitempty"`
	StartFire    int `tdf:"startfire,omitempty"`

	Coverage int `tdf:"coverage,omitempty"`

	EnergyPerShot float64 `tdf:"energypershot,omitempty"`
	MetalPerShot  int     `tdf:"metalpershot,omitempty"`
	Energy        int     `tdf:"energy,omitempty"` // build/stockpile cost
	Metal         int     `tdf:"metal,omitempty"`  // build/stockpile cost
	WeaponTimer   float64 `tdf:"weapontimer,omitempty"`

	WeaponAcceleration float64 `tdf:"weaponacceleration,omitempty"`
	StartVelocity      float64 `tdf:"startvelocity,omitempty"`
	Duration           float64 `tdf:"duration,omitempty"`

	Burst       int     `tdf:"burst,omitempty"`
	BurstRate   float64 `tdf:"burstrate,omitempty"`
	SprayAngle  int     `tdf:"sprayangle,omitempty"`
	RandomDecay float64 `tdf:"randomdecay,omitempty"`

	FlightTime     float64 `tdf:"flighttime,omitempty"`
	Accuracy       int     `tdf:"accuracy,omitempty"`
	Tolerance      int     `tdf:"tolerance,omitempty"`
	PitchTolerance int     `tdf:"pitchtolerance,omitempty"`
	AimRate        int     `tdf:"aimrate,omitempty"`
	HoldTime       int     `tdf:"holdtime,omitempty"`

	MinBarrelAngle float64 `tdf:"minbarrelangle,omitempty"` // degrees

	// Visuals.
	Color             int     `tdf:"color,omitempty"`
	Color2            int     `tdf:"color2,omitempty"`
	SmokeTrail        int     `tdf:"smoketrail,omitempty"`
	SmokeDelay        float64 `tdf:"smokedelay,omitempty"`
	StartSmoke        int     `tdf:"startsmoke,omitempty"`
	EndSmoke          int     `tdf:"endsmoke,omitempty"`
	BeamWeapon        int     `tdf:"beamweapon,omitempty"`
	ExplosionGAF      string  `tdf:"explosiongaf,omitempty"`
	ExplosionArt      string  `tdf:"explosionart,omitempty"`
	WaterExplosionGAF string  `tdf:"waterexplosiongaf,omitempty"`
	WaterExplosionArt string  `tdf:"waterexplosionart,omitempty"`
	LavaExplosionGAF  string  `tdf:"lavaexplosiongaf,omitempty"`
	LavaExplosionArt  string  `tdf:"lavaexplosionart,omitempty"`
	Propeller         int     `tdf:"propeller,omitempty"`
	ShakeMagnitude    int     `tdf:"shakemagnitude,omitempty"`
	ShakeDuration     float64 `tdf:"shakeduration,omitempty"`

	// Sounds.
	SoundStart   string `tdf:"soundstart,omitempty"`
	SoundWater   string `tdf:"soundwater,omitempty"`
	SoundTrigger int    `tdf:"soundtrigger,omitempty"`

	// Per-target-category damage values: [DAMAGE]{ default=10; corpyro=2; }.
	// Values read with Atol, so "13O" is 13 and a bad value never fails the
	// file; Meta keeps each entry's text and order. The game keys entries
	// by unit name and keeps each value to 16 bits (see EffectiveDamage).
	// It ignores sections nested in [DAMAGE]. The map cannot hold them, so
	// the Meta keeps them and Marshal writes them back unchanged, in place.
	Damage map[string]int `tdf:"damage,omitempty"`
}

// Weapon satisfies the shared common.Weapon interface via its embedded base.
var _ common.Weapon = (*Weapon)(nil)
