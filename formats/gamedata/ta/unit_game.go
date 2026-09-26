package ta

import (
	"fmt"
	"strings"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// Defaults TA 3.1c applies to [UNITINFO] keys a unit file leaves out, and the
// widths it keeps some values to.
const (
	// DefaultStandingOrder is the standing move and fire order of a unit
	// whose file has no StandingMoveOrder or StandingFireOrder: Roam and Fire
	// at Will. An explicit value is kept to 2 bits (0 Hold Position or Hold
	// Fire, 1 Maneuver or Return Fire, 2 Roam or Fire at Will).
	DefaultStandingOrder = 2
	// DefaultSelfDestructCountdown is the countdown, in seconds, of a unit
	// whose file has no SelfDestructCountdown. An explicit value is kept to
	// 3 bits (0..7).
	DefaultSelfDestructCountdown = 5
	// DefaultMinCloakDistance is the decloak distance of a cloaking unit
	// (CloakCost above 0) whose MinCloakDistance is missing or 0.
	DefaultMinCloakDistance = 80
	// DefaultBadTargetCategory is the category of a unit's missing
	// wpri_/wsec_/wspe_badTargetCategory and NoChaseCategory keys.
	DefaultBadTargetCategory = "none"

	// UnitNameMax, NameMax, DescriptionMax and ObjectNameMax are the longest
	// UnitName, Name, Description and ObjectName the game keeps; longer text
	// is cut to that many bytes.
	UnitNameMax    = 31
	NameMax        = 31
	DescriptionMax = 63
	ObjectNameMax  = 31
	// CategoryTextMax is the longest text the game reads from Category and
	// the target-category keys.
	CategoryTextMax = 99
)

// present reports whether a typed key counts as present (see values.Present).
func (u *UnitInfo) present(key string, nonZero bool) bool {
	return values.Present(&u.Meta, key, nonZero)
}

// remaining returns the text of a key held in the catch-all.
func (u *UnitInfo) remaining(key string) (string, bool) {
	return values.Lookup(u.Remaining, key)
}

// EffectiveObjectName returns the model name the game loads: ObjectName, or
// UnitName when the file has no ObjectName key, cut to ObjectNameMax bytes.
func (u *UnitInfo) EffectiveObjectName() string {
	if u.present("objectname", u.ObjectName != "") {
		return values.Truncate(u.ObjectName, ObjectNameMax)
	}
	return values.Truncate(u.UnitName, UnitNameMax)
}

// EffectiveStandingMoveOrder returns the unit's initial move order: 0 Hold
// Position, 1 Maneuver, 2 Roam. A missing key gives DefaultStandingOrder; an
// explicit value keeps its low 2 bits, so an explicit 0 is Hold Position.
func (u *UnitInfo) EffectiveStandingMoveOrder() int {
	if u.present("standingmoveorder", u.StandingMoveOrder != 0) {
		return int(uint32(u.StandingMoveOrder) & 3)
	}
	return DefaultStandingOrder
}

// EffectiveStandingFireOrder returns the unit's initial fire order: 0 Hold
// Fire, 1 Return Fire, 2 Fire at Will, with the same rules as
// EffectiveStandingMoveOrder.
func (u *UnitInfo) EffectiveStandingFireOrder() int {
	if u.present("standingfireorder", u.StandingFireOrder != 0) {
		return int(uint32(u.StandingFireOrder) & 3)
	}
	return DefaultStandingOrder
}

// EffectiveSelfDestructCountdown returns the self-destruct countdown in
// seconds: DefaultSelfDestructCountdown when the key is missing, otherwise
// its low 3 bits (so 9 is 1).
func (u *UnitInfo) EffectiveSelfDestructCountdown() int {
	if u.present("selfdestructcountdown", u.SelfDestructCountdown != 0) {
		return int(uint32(u.SelfDestructCountdown) & 7)
	}
	return DefaultSelfDestructCountdown
}

// fixed returns a float field's 16.16 fixed-point reading, as the game stores
// motion values.
func (u *UnitInfo) fixed(key string, v float64) float64 {
	return values.FixedOfFloat(&u.Meta, u.Remaining, key, v)
}

// EffectiveBankScale returns BankScale as the game reads it (16.16 fixed
// point), or 1 when the key is missing.
func (u *UnitInfo) EffectiveBankScale() float64 {
	if u.present("bankscale", u.BankScale != 0) {
		return u.fixed("bankscale", u.BankScale)
	}
	return 1
}

// EffectiveDamageModifier returns DamageModifier as the game reads it (16.16
// fixed point), or 1 when the key is missing.
func (u *UnitInfo) EffectiveDamageModifier() float64 {
	if u.present("damagemodifier", u.DamageModifier != 0) {
		return u.fixed("damagemodifier", u.DamageModifier)
	}
	return 1
}

// EffectiveMaxVelocity returns MaxVelocity as the game reads it (16.16 fixed
// point).
func (u *UnitInfo) EffectiveMaxVelocity() float64 {
	return u.fixed("maxvelocity", u.MaxVelocity)
}

// doubledVelocity is the game's default move rate: the 16.16 maximum velocity
// shifted left one bit, wrapping to 32 bits.
func (u *UnitInfo) doubledVelocity() float64 {
	mv := int32(u.EffectiveMaxVelocity() * 65536)
	return float64(int32(uint32(mv)<<1)) / 65536
}

// EffectiveMoveRate1 returns MoveRate1 as the game reads it (16.16 fixed
// point), or twice the maximum velocity when the key is missing.
func (u *UnitInfo) EffectiveMoveRate1() float64 {
	if u.present("moverate1", u.MoveRate1 != 0) {
		return u.fixed("moverate1", u.MoveRate1)
	}
	return u.doubledVelocity()
}

// EffectiveMoveRate2 returns MoveRate2 with the same rules as
// EffectiveMoveRate1.
func (u *UnitInfo) EffectiveMoveRate2() float64 {
	if u.present("moverate2", u.MoveRate2 != 0) {
		return u.fixed("moverate2", u.MoveRate2)
	}
	return u.doubledVelocity()
}

// category returns a category key's text as the game reads it, or
// DefaultBadTargetCategory when the key is missing.
func (u *UnitInfo) category(key string, typed *string) string {
	if typed != nil {
		if u.present(key, *typed != "") {
			return values.Truncate(*typed, CategoryTextMax)
		}
		return DefaultBadTargetCategory
	}
	if t, ok := u.remaining(key); ok {
		return values.Truncate(t, CategoryTextMax)
	}
	return DefaultBadTargetCategory
}

// EffectiveBadTargetCategories returns the categories the unit's primary,
// secondary and special weapons avoid (wpri_, wsec_ and
// wspe_badTargetCategory), each DefaultBadTargetCategory when missing. The
// game reads no plain badTargetCategory key.
func (u *UnitInfo) EffectiveBadTargetCategories() (primary, secondary, special string) {
	return u.category("wpri_badtargetcategory", &u.WpriBadTargetCategory),
		u.category("wsec_badtargetcategory", &u.WsecBadTargetCategory),
		u.category("wspe_badtargetcategory", nil)
}

// EffectiveNoChaseCategory returns the categories the unit does not chase, or
// DefaultBadTargetCategory when the key is missing.
func (u *UnitInfo) EffectiveNoChaseCategory() string {
	return u.category("nochasecategory", &u.NoChaseCategory)
}

// EffectiveCloakCost returns CloakCost as the game reads it: a whole number
// (Atol), so 12.5 is 12. A unit can cloak when it is above 0.
func (u *UnitInfo) EffectiveCloakCost() int {
	return int(values.IntOfFloat(&u.Meta, u.Remaining, "cloakcost", u.CloakCost))
}

// EffectiveCloakCostMoving returns CloakCostMoving as a whole number, or the
// whole CloakCost when the key is missing.
func (u *UnitInfo) EffectiveCloakCostMoving() int {
	if u.present("cloakcostmoving", u.CloakCostMoving != 0) {
		return int(values.IntOfFloat(&u.Meta, u.Remaining, "cloakcostmoving", u.CloakCostMoving))
	}
	return u.EffectiveCloakCost()
}

// EffectiveMinCloakDistance returns MinCloakDistance kept to 16 bits, or
// DefaultMinCloakDistance for a unit that can cloak when that is 0.
func (u *UnitInfo) EffectiveMinCloakDistance() int {
	d := int(int16(u.MinCloakDistance))
	if d == 0 && u.EffectiveCloakCost() > 0 {
		return DefaultMinCloakDistance
	}
	return d
}

// EffectiveBuildCostEnergy returns BuildCostEnergy as the game reads it, a
// whole number.
func (u *UnitInfo) EffectiveBuildCostEnergy() int {
	return u.BuildCostEnergy
}

// EffectiveBuildCostMetal returns BuildCostMetal as the game reads it: a
// whole number (Atol), so 321.9 is 321.
func (u *UnitInfo) EffectiveBuildCostMetal() int {
	return int(values.IntOfFloat(&u.Meta, u.Remaining, "buildcostmetal", u.BuildCostMetal))
}

// EffectiveEnergyStorage returns EnergyStorage as the game reads it, a
// fraction (Atof) where the field holds only its whole part.
func (u *UnitInfo) EffectiveEnergyStorage() float64 {
	return values.Float(&u.Meta, u.Remaining, "energystorage", u.EnergyStorage)
}

// EffectiveMetalStorage returns MetalStorage as a fraction (see
// EffectiveEnergyStorage).
func (u *UnitInfo) EffectiveMetalStorage() float64 {
	return values.Float(&u.Meta, u.Remaining, "metalstorage", u.MetalStorage)
}

// SetEnergyStorage sets energystorage to v, which may be fractional: Marshal
// writes it exactly and EnergyStorage holds its whole part.
func (u *UnitInfo) SetEnergyStorage(v float64) {
	values.SetFloat(&u.Meta, &u.Remaining, "energystorage", &u.EnergyStorage, v)
}

// SetMetalStorage sets metalstorage to v (see SetEnergyStorage).
func (u *UnitInfo) SetMetalStorage(v float64) {
	values.SetFloat(&u.Meta, &u.Remaining, "metalstorage", &u.MetalStorage, v)
}

// EffectiveMakesMetal returns MakesMetal as the game stores it: a whole number
// (Atol) kept to 8 bits, signed.
func (u *UnitInfo) EffectiveMakesMetal() int {
	return int(int8(values.IntOfFloat(&u.Meta, u.Remaining, "makesmetal", u.MakesMetal)))
}

// EffectiveWaterLine returns WaterLine as the game stores it: a whole number
// kept to 8 bits, signed, so 300 is 44.
func (u *UnitInfo) EffectiveWaterLine() int {
	return int(int8(values.IntOfFloat(&u.Meta, u.Remaining, "waterline", u.WaterLine)))
}

// EffectiveCruiseAlt returns CruiseAlt as the game stores it: a whole number
// kept to 16 bits, signed.
func (u *UnitInfo) EffectiveCruiseAlt() int {
	return int(int16(values.IntOfFloat(&u.Meta, u.Remaining, "cruisealt", u.CruiseAlt)))
}

// ownLimits returns the movement limits of the unit's own [UNITINFO] keys, as
// the game uses them for a unit with no (or an unknown) movement class.
func (u *UnitInfo) ownLimits() MovementLimits {
	typed := map[string]int{
		"footprintx": u.FootprintX, "footprintz": u.FootprintZ,
		"maxwaterdepth": u.MaxWaterDepth, "minwaterdepth": u.MinWaterDepth,
		"maxslope": u.MaxSlope,
	}
	return resolveLimits(func(key string) intKey {
		if v, ok := typed[key]; ok {
			return intKey{int32(v), u.present(key, v != 0)}
		}
		if t, ok := u.remaining(key); ok {
			return intKey{tdf.Atol(t), true}
		}
		return intKey{}
	})
}

// Movement returns the unit's footprint and terrain limits as the game
// resolves them, and the slot of the movement class it uses (-1 for none).
//
// When the unit names a class (MovementClass, found with FindMovementClass
// among classes, the decoded gamedata/moveinfo.tdf), the class's values
// replace the unit's own footprint, water depth and slope keys entirely, and a
// key the class leaves out takes the game's default (see
// MovementClass.Limits). Otherwise, including when the name matches no class,
// the unit's own keys are read with the same defaults, so a unit with no
// MaxWaterDepth key has 10000.
func (u *UnitInfo) Movement(classes []MovementClass) (MovementLimits, int) {
	if u.present("movementclass", u.MovementClass != "") {
		if c, slot := FindMovementClass(classes, u.MovementClass); c != nil {
			return c.Limits(), slot
		}
	}
	return u.ownLimits(), -1
}

// Check reports values in the unit file that the game reads differently from
// how they look: text longer than the game keeps, orders and countdowns
// outside the bits it keeps, a WaterLine outside 8 bits, weapon names too long
// to ever match a weapon, and sections the game never reads.
func (u *Unit) Check() []common.Warning {
	var out []common.Warning
	info := &u.Info
	section := "UNITINFO"
	if info.Key != "" {
		section = info.Key
	}
	warn := func(key, format string, args ...any) {
		out = append(out, common.Warning{Section: section, Key: key, Message: fmt.Sprintf(format, args...)})
	}
	if !u.Meta.HasSection("UNITINFO") && (len(u.Sections) > 0 || len(u.Remaining) > 0) {
		out = append(out, common.Warning{Message: "no [UNITINFO] section; the game cannot load the unit"})
	}
	for _, s := range u.Sections {
		if values.EqualFold(s.Key, "UNITINFO") {
			out = append(out, common.Warning{Section: s.Key, Message: "a second [UNITINFO]; the game reads the first"})
		}
	}
	for _, f := range []struct {
		key, v string
		max    int
	}{
		{"UnitName", info.UnitName, UnitNameMax},
		{"Name", info.Name, NameMax},
		{"Description", info.Description, DescriptionMax},
		{"ObjectName", info.ObjectName, ObjectNameMax},
		{"Category", strings.Join(info.Category, " "), CategoryTextMax},
	} {
		if len(f.v) > f.max {
			warn(f.key, "%d characters; the game keeps the first %d (%q)", len(f.v), f.max, values.Truncate(f.v, f.max))
		}
	}
	for _, f := range []struct {
		key string
		v   int
	}{{"StandingMoveOrder", info.StandingMoveOrder}, {"StandingFireOrder", info.StandingFireOrder}} {
		if f.v < 0 || f.v > 3 {
			warn(f.key, "%d is outside 0..3; the game keeps the low 2 bits (%d)", f.v, uint32(f.v)&3)
		}
	}
	if v := info.SelfDestructCountdown; v < 0 || v > 7 {
		warn("SelfDestructCountdown", "%d is outside 0..7; the game keeps the low 3 bits (%d)", v, uint32(v)&7)
	}
	if wl := values.IntOfFloat(&info.Meta, info.Remaining, "waterline", info.WaterLine); wl < -128 || wl > 127 {
		warn("WaterLine", "%d is outside -128..127; the game keeps 8 bits (%d)", wl, int8(wl))
	}
	for _, f := range []struct{ key, v string }{
		{"Weapon1", info.Weapon1}, {"Weapon2", info.Weapon2}, {"Weapon3", info.Weapon3},
		{"ExplodeAs", info.ExplodeAs}, {"SelfDestructAs", info.SelfDestructAs},
	} {
		if len(f.v) > WeaponKeyMax {
			warn(f.key, "%d characters; weapon section names are cut to %d, so the game never finds %q",
				len(f.v), WeaponKeyMax, f.v)
		}
	}
	return out
}
