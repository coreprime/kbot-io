package ta

import (
	"fmt"
	"path"
	"strings"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
)

const (
	// WeaponSlots is the size of the game's weapon table; a weapon's ID is
	// its slot, 0..255.
	WeaponSlots = 256
	// WeaponKeyMax is the longest weapon section name the game keeps. A
	// unit's Weapon1 (and the other weapon references) must match the name
	// cut to this length, so a longer reference never matches.
	WeaponKeyMax = 31
	// WeaponNameMax is the longest name= text the game keeps.
	WeaponNameMax = 63
	// DefaultWeaponRange is the range of a weapon with no range key.
	DefaultWeaponRange = 0x7fff
	// DefaultMinBarrelAngle is the minimum barrel angle, in degrees, of a
	// weapon with no minbarrelangle key.
	DefaultMinBarrelAngle = -11.25
)

// EffectiveID returns the weapon's slot in the game's table and whether the
// game loads the section at all: it needs an ID key with a value in 0..255.
func (w *Weapon) EffectiveID() (int, bool) {
	if !values.Present(&w.Meta, "id", w.ID != 0) {
		return 0, false
	}
	return w.ID, w.ID >= 0 && w.ID < WeaponSlots
}

// SetID sets the weapon's ID and records the key as present, so Marshal
// writes it even when it is 0.
func (w *Weapon) SetID(id int) {
	w.ID = id
	w.Meta.Mark("id")
}

// EffectiveRange returns the weapon's range: Range, or DefaultWeaponRange when
// the key is missing (an explicit 0 stays 0).
func (w *Weapon) EffectiveRange() int {
	if values.Present(&w.Meta, "range", w.Range != 0) {
		return w.Range
	}
	return DefaultWeaponRange
}

// EffectiveMinBarrelAngle returns the minimum barrel angle in degrees:
// MinBarrelAngle, or DefaultMinBarrelAngle when the key is missing.
func (w *Weapon) EffectiveMinBarrelAngle() float64 {
	if values.Present(&w.Meta, "minbarrelangle", w.MinBarrelAngle != 0) {
		return w.MinBarrelAngle
	}
	return DefaultMinBarrelAngle
}

// EffectiveTurnRate returns the turn rate as the game reads it, a fraction
// (angle units per second) where TurnRate holds only its whole part.
func (w *Weapon) EffectiveTurnRate() float64 {
	return values.Float(&w.Meta, w.Remaining, "turnrate", w.TurnRate)
}

// EffectiveMetalPerShot returns the metal each shot costs as the game reads
// it, a fraction where MetalPerShot holds only its whole part (0.5 reads as 0
// in the field and 0.5 here).
func (w *Weapon) EffectiveMetalPerShot() float64 {
	return values.Float(&w.Meta, w.Remaining, "metalpershot", w.MetalPerShot)
}

// EffectiveHoldTime returns the hold time in seconds as the game reads it, a
// fraction where HoldTime holds only its whole part.
func (w *Weapon) EffectiveHoldTime() float64 {
	return values.Float(&w.Meta, w.Remaining, "holdtime", w.HoldTime)
}

// EffectiveFireStarter returns FireStarter as the game stores it: a whole
// number (Atol) kept to 8 bits, signed.
func (w *Weapon) EffectiveFireStarter() int {
	return int(int8(values.IntOfFloat(&w.Meta, w.Remaining, "firestarter", w.FireStarter)))
}

// EffectiveDamage returns the damage the weapon does to a unit: the [DAMAGE]
// entry for the unit's name (ignoring case), else its default= entry, else 0,
// each kept to 16 bits, signed, as the game stores them.
func (w *Weapon) EffectiveDamage(unitName string) int {
	def := 0
	for k, v := range w.Damage {
		if values.EqualFold(k, unitName) && !values.EqualFold(k, "default") {
			return int(int16(v))
		}
		if values.EqualFold(k, "default") {
			def = int(int16(v))
		}
	}
	return def
}

// SetTurnRate sets turnrate to v, which may be fractional: Marshal writes it
// exactly and TurnRate holds its whole part.
func (w *Weapon) SetTurnRate(v float64) {
	values.SetFloat(&w.Meta, &w.Remaining, "turnrate", &w.TurnRate, v)
}

// SetMetalPerShot sets metalpershot to v (see SetTurnRate).
func (w *Weapon) SetMetalPerShot(v float64) {
	values.SetFloat(&w.Meta, &w.Remaining, "metalpershot", &w.MetalPerShot, v)
}

// SetHoldTime sets holdtime to v (see SetTurnRate).
func (w *Weapon) SetHoldTime(v float64) {
	values.SetFloat(&w.Meta, &w.Remaining, "holdtime", &w.HoldTime, v)
}

// IsWeaponFile reports whether the game loads weapons from the file at p, a
// path relative to the game's root: a .tdf file directly in the weapons
// directory (either slash, any case). gamedata/weapons.tdf and files in
// subdirectories of weapons are not weapon sources.
func IsWeaponFile(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	dir, file := path.Split(p)
	return values.EqualFold(strings.TrimSuffix(dir, "/"), "weapons") &&
		len(file) > len(".tdf") && values.EqualFold(path.Ext(file), ".tdf")
}

// WeaponTable is the game's weapon table: WeaponSlots slots, each holding the
// weapon whose ID names it. Build it with Add, one weapons/*.tdf file at a
// time in the order the game lists them.
type WeaponTable struct {
	// Slots holds the weapon in each slot, nil when none has that ID.
	Slots [WeaponSlots]*Weapon
	// Files holds, for each filled slot, the name Add was given for the file
	// the weapon came from.
	Files [WeaponSlots]string
	// Warnings lists the sections the game skips or replaces, and names it
	// cannot resolve, in the order Add met them.
	Warnings []common.Warning
}

// NewWeaponTable returns an empty table.
func NewWeaponTable() *WeaponTable { return &WeaponTable{} }

// Add loads one file's weapons, in order. A weapon goes into the slot its ID
// names; a later weapon with the same ID, in this file or a later one,
// replaces it (with a warning). A section with no ID, or an ID outside 0..255,
// is skipped with a warning. file names the file in warnings.
func (t *WeaponTable) Add(file string, weapons []Weapon) {
	for i := range weapons {
		w := &weapons[i]
		section := w.Key
		id, ok := w.EffectiveID()
		switch {
		case !values.Present(&w.Meta, "id", w.ID != 0):
			t.warn(file, section, "no ID key; the game skips the section")
			continue
		case !ok:
			t.warn(file, section, fmt.Sprintf("ID %d is outside 0..%d; the game skips the section", id, WeaponSlots-1))
			continue
		}
		if prev := t.Slots[id]; prev != nil {
			t.warn(file, section, fmt.Sprintf("ID %d replaces [%s] from %s", id, prev.Key, t.Files[id]))
		}
		if w.Key == "" {
			t.warn(file, section, "the section has no name, so no unit can refer to it")
		} else if len(w.Key) > WeaponKeyMax {
			t.warn(file, section, fmt.Sprintf("the name is %d characters; the game keeps %q",
				len(w.Key), values.Truncate(w.Key, WeaponKeyMax)))
		}
		t.Slots[id] = w
		t.Files[id] = file
	}
}

func (t *WeaponTable) warn(file, section, msg string) {
	t.Warnings = append(t.Warnings, common.Warning{Section: file + ": " + section, Message: msg})
}

// ByID returns the weapon in slot id, or nil.
func (t *WeaponTable) ByID(id int) *Weapon {
	if id < 0 || id >= WeaponSlots {
		return nil
	}
	return t.Slots[id]
}

// Find returns the weapon a unit's Weapon1 (or Weapon2, Weapon3, ExplodeAs,
// SelfDestructAs) value names, and its slot: the lowest slot whose section
// name, cut to WeaponKeyMax characters, equals name ignoring case. An empty
// name, or one matching no slot, gives nil and -1 (the game's "no weapon").
func (t *WeaponTable) Find(name string) (*Weapon, int) {
	if name == "" {
		return nil, -1
	}
	for id, w := range t.Slots {
		if w != nil && values.EqualFold(values.Truncate(w.Key, WeaponKeyMax), name) {
			return w, id
		}
	}
	return nil, -1
}

// CheckWeapons reports what the game skips or reads differently in one
// weapons file: sections without a valid ID, IDs used twice (the later
// section wins), section names too long to be referred to and name= text
// longer than the game keeps.
func CheckWeapons(weapons []Weapon) []common.Warning {
	t := NewWeaponTable()
	t.Add("", weapons)
	out := make([]common.Warning, 0, len(t.Warnings))
	for _, w := range t.Warnings {
		w.Section = strings.TrimPrefix(w.Section, ": ")
		out = append(out, w)
	}
	for i := range weapons {
		if n := weapons[i].Name; len(n) > WeaponNameMax {
			out = append(out, common.Warning{Section: weapons[i].Key, Key: "name", Message: fmt.Sprintf(
				"%d characters; the game keeps the first %d", len(n), WeaponNameMax)})
		}
	}
	return out
}
