package ta

import (
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

func decodeWeapons(t *testing.T, src string) []Weapon {
	t.Helper()
	var ws []Weapon
	if err := tdf.Unmarshal([]byte(src), &ws); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ws
}

// The game reads numbers like atoi/atof: the stock typo "13O" is 13, and a
// bad [DAMAGE] value reads its leading number instead of failing the file.
func TestWeaponNumbersReadLikeTheGame(t *testing.T) {
	src := `[ARMVTOL_ADVMISSILE2]
{
	ID=5;
	weaponacceleration=13O;
	range=1e3;
	[DAMAGE]
	{
		default=13O;
		armcom=50;
		corcom=abc;
	}
}
[OTHER] { ID=6; }`
	ws := decodeWeapons(t, src)
	if len(ws) != 2 {
		t.Fatalf("weapons: %d", len(ws))
	}
	w := &ws[0]
	if w.WeaponAcceleration != 13 || w.Range != 1 {
		t.Errorf("acceleration %v range %d, want 13 and 1", w.WeaponAcceleration, w.Range)
	}
	if w.Damage["default"] != 13 || w.Damage["corcom"] != 0 {
		t.Errorf("damage: %v", w.Damage)
	}
	if w.EffectiveDamage("ARMCOM") != 50 || w.EffectiveDamage("armflea") != 13 {
		t.Errorf("effective damage: %d %d", w.EffectiveDamage("ARMCOM"), w.EffectiveDamage("armflea"))
	}
	out := marshal(t, ws)
	for _, want := range []string{"weaponacceleration=13O;", "default=13O;", "corcom=abc;", "range=1e3;"} {
		if !strings.Contains(out, want) {
			t.Errorf("text lost %q:\n%s", want, out)
		}
	}
}

func TestWeaponDefaultsAndFractions(t *testing.T) {
	ws := decodeWeapons(t, `[A]{ID=1;turnrate=16384.5;metalpershot=0.5;holdtime=1.5;firestarter=300;}
[B]{ID=2;range=0;minbarrelangle=0;}`)
	a, b := &ws[0], &ws[1]
	if a.EffectiveRange() != DefaultWeaponRange || a.EffectiveMinBarrelAngle() != DefaultMinBarrelAngle {
		t.Errorf("defaults: %d %v", a.EffectiveRange(), a.EffectiveMinBarrelAngle())
	}
	if b.EffectiveRange() != 0 || b.EffectiveMinBarrelAngle() != 0 {
		t.Errorf("explicit zeros: %d %v", b.EffectiveRange(), b.EffectiveMinBarrelAngle())
	}
	if a.EffectiveTurnRate() != 16384.5 || a.EffectiveMetalPerShot() != 0.5 || a.EffectiveHoldTime() != 1.5 {
		t.Errorf("fractions: %v %v %v", a.EffectiveTurnRate(), a.EffectiveMetalPerShot(), a.EffectiveHoldTime())
	}
	if a.MetalPerShot != 0 || a.HoldTime != 1 {
		t.Errorf("int fields: %d %d", a.MetalPerShot, a.HoldTime)
	}
	if a.EffectiveFireStarter() != 44 {
		t.Errorf("fire starter 300: %d", a.EffectiveFireStarter())
	}
	out := marshal(t, ws)
	for _, want := range []string{"metalpershot=0.5;", "holdtime=1.5;", "range=0;", "minbarrelangle=0;"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q:\n%s", want, out)
		}
	}
	a.SetMetalPerShot(0.25)
	if a.EffectiveMetalPerShot() != 0.25 || !strings.Contains(marshal(t, ws), "metalpershot=0.25;") {
		t.Errorf("SetMetalPerShot: %v\n%s", a.EffectiveMetalPerShot(), marshal(t, ws))
	}
}

func TestWeaponIDStaysAbsent(t *testing.T) {
	ws := decodeWeapons(t, `[NOID]{name=x;}[ZERO]{ID=0;}`)
	out := marshal(t, ws)
	if strings.Count(strings.ToLower(out), "id=") != 1 || !strings.Contains(out, "ID=0;") {
		t.Errorf("IDs:\n%s", out)
	}
	if _, ok := ws[0].EffectiveID(); ok {
		t.Error("a section with no ID is loaded")
	}
	if id, ok := ws[1].EffectiveID(); !ok || id != 0 {
		t.Errorf("explicit ID 0: %d %v", id, ok)
	}
	var w Weapon
	w.Key = "NEW"
	w.SetID(0)
	if !strings.Contains(marshal(t, []Weapon{w}), "id=0;") {
		t.Errorf("SetID(0) not written:\n%s", marshal(t, []Weapon{w}))
	}
}

func TestWeaponTableFollowsTheGame(t *testing.T) {
	long := strings.Repeat("L", 35)
	first := decodeWeapons(t, `[LASER]{ID=3;range=100;}
[NOID]{range=1;}
[BIG]{ID=300;}
[`+long+`]{ID=9;}
[laser]{ID=7;}`)
	second := decodeWeapons(t, `[CANNON]{ID=3;range=200;}[LASER]{ID=8;}`)
	table := NewWeaponTable()
	table.Add("weapons/a.tdf", first)
	table.Add("weapons/b.tdf", second)
	if w := table.ByID(3); w == nil || w.Key != "CANNON" {
		t.Errorf("slot 3: %+v, want the later file's CANNON", w)
	}
	if w, id := table.Find("Laser"); w == nil || id != 7 {
		t.Errorf("Laser resolves to slot %d, want the lowest slot holding it (7)", id)
	}
	if w, _ := table.Find(long); w != nil {
		t.Error("a reference longer than the kept name resolved")
	}
	if w, id := table.Find(long[:WeaponKeyMax]); w == nil || id != 9 {
		t.Errorf("the cut name: slot %d", id)
	}
	if w, _ := table.Find(""); w != nil {
		t.Error("an empty name resolved")
	}
	var msgs []string
	for _, w := range table.Warnings {
		msgs = append(msgs, w.String())
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"NOID", "no ID key", "ID 300 is outside", "replaces [LASER]", "35 characters"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings miss %q:\n%s", want, joined)
		}
	}
	if len(CheckWeapons(first)) != 3 {
		t.Errorf("CheckWeapons: %v", CheckWeapons(first))
	}
}

func TestIsWeaponFile(t *testing.T) {
	for p, want := range map[string]bool{
		"weapons/weapons.tdf":   true,
		"WEAPONS\\Missiles.TDF": true,
		"gamedata/weapons.tdf":  false,
		"weapons/old/x.tdf":     false,
		"weapons/x.fbi":         false,
		"weapons/.tdf":          false,
	} {
		if got := IsWeaponFile(p); got != want {
			t.Errorf("IsWeaponFile(%q) = %v", p, got)
		}
	}
}
