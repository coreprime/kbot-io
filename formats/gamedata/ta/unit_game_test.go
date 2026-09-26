package ta

import (
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

func decodeUnit(t *testing.T, src string) *Unit {
	t.Helper()
	var u Unit
	if err := tdf.Unmarshal([]byte(src), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &u
}

// An explicit zero whose game default is not zero must survive a round trip:
// dropping MaxWaterDepth=0 would let the solar collector be built in deep
// water, and dropping StandingFireOrder=0 would make a hold-fire unit fire at
// will.
func TestUnitKeepsExplicitZeros(t *testing.T) {
	src := `[UNITINFO]
{
	UnitName=ARMSOLAR;
	MaxWaterDepth=0;
	StandingFireOrder=0;
	StandingMoveOrder=0;
	SelfDestructCountdown=0;
	ObjectName=;
	BankScale=0;
}`
	u := decodeUnit(t, src)
	out := marshal(t, u)
	for _, want := range []string{"MaxWaterDepth=0;", "StandingFireOrder=0;", "StandingMoveOrder=0;",
		"SelfDestructCountdown=0;", "ObjectName=;", "BankScale=0;"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost %q:\n%s", want, out)
		}
	}
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("round trip differs: %s", msg)
	}
	info := &u.Info
	if got := info.EffectiveStandingFireOrder(); got != 0 {
		t.Errorf("standing fire order: %d, want 0 (hold fire)", got)
	}
	if got := info.EffectiveSelfDestructCountdown(); got != 0 {
		t.Errorf("self-destruct countdown: %d, want 0", got)
	}
	if got := info.EffectiveObjectName(); got != "" {
		t.Errorf("object name: %q, want empty", got)
	}
	if got := info.EffectiveBankScale(); got != 0 {
		t.Errorf("bank scale: %v, want 0", got)
	}
	if l, slot := info.Movement(nil); l.MaxWaterDepth != 0 || slot != -1 {
		t.Errorf("movement: %+v slot %d, want max water depth 0 from the unit's own key", l, slot)
	}
}

func TestUnitDefaultsForMissingKeys(t *testing.T) {
	u := decodeUnit(t, `[UNITINFO]{UnitName=ARMFOO;MaxVelocity=1.5;CloakCost=12.9;}`)
	info := &u.Info
	if got := info.EffectiveStandingMoveOrder(); got != DefaultStandingOrder {
		t.Errorf("standing move order: %d", got)
	}
	if got := info.EffectiveStandingFireOrder(); got != DefaultStandingOrder {
		t.Errorf("standing fire order: %d", got)
	}
	if got := info.EffectiveSelfDestructCountdown(); got != DefaultSelfDestructCountdown {
		t.Errorf("self-destruct: %d", got)
	}
	if got := info.EffectiveObjectName(); got != "ARMFOO" {
		t.Errorf("object name: %q", got)
	}
	if info.EffectiveBankScale() != 1 || info.EffectiveDamageModifier() != 1 {
		t.Errorf("bank scale %v, damage modifier %v; want 1", info.EffectiveBankScale(), info.EffectiveDamageModifier())
	}
	if got := info.EffectiveMoveRate1(); got != 3 {
		t.Errorf("move rate 1: %v, want twice the max velocity", got)
	}
	p, s, sp := info.EffectiveBadTargetCategories()
	if p != "none" || s != "none" || sp != "none" || info.EffectiveNoChaseCategory() != "none" {
		t.Errorf("categories: %q %q %q %q", p, s, sp, info.EffectiveNoChaseCategory())
	}
	if got := info.EffectiveCloakCost(); got != 12 {
		t.Errorf("cloak cost: %d, want the whole number 12", got)
	}
	if got := info.EffectiveCloakCostMoving(); got != 12 {
		t.Errorf("cloak cost moving: %d, want the cloak cost", got)
	}
	if got := info.EffectiveMinCloakDistance(); got != DefaultMinCloakDistance {
		t.Errorf("min cloak distance: %d", got)
	}
	l, _ := info.Movement(nil)
	want := MovementLimits{MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 255, BadSlope: 127, MaxWaterSlope: 255, BadWaterSlope: 127}
	if l != want {
		t.Errorf("movement: %+v, want %+v", l, want)
	}
}

func TestUnitStandingOrdersKeepTwoBits(t *testing.T) {
	u := decodeUnit(t, `[UNITINFO]{StandingMoveOrder=5;StandingFireOrder=6;SelfDestructCountdown=9;}`)
	if got := u.Info.EffectiveStandingMoveOrder(); got != 1 {
		t.Errorf("move order 5: %d, want 1", got)
	}
	if got := u.Info.EffectiveStandingFireOrder(); got != 2 {
		t.Errorf("fire order 6: %d, want 2", got)
	}
	if got := u.Info.EffectiveSelfDestructCountdown(); got != 1 {
		t.Errorf("countdown 9: %d, want 1", got)
	}
	warnings := u.Check()
	if len(warnings) != 3 {
		t.Errorf("warnings: %v", warnings)
	}
}

func TestUnitNarrowsAndReadsNumbersLikeTheGame(t *testing.T) {
	u := decodeUnit(t, `[UNITINFO]{BuildCostMetal=321.9;WaterLine=300;EnergyStorage=0.5;MetalStorage=1e3;CruiseAlt=70000;MakesMetal=1.9;}`)
	info := &u.Info
	if got := info.EffectiveBuildCostMetal(); got != 321 {
		t.Errorf("build cost metal: %d", got)
	}
	if got := info.EffectiveWaterLine(); got != 44 {
		t.Errorf("waterline 300: %d, want 44", got)
	}
	if got := info.EffectiveEnergyStorage(); got != 0.5 {
		t.Errorf("energy storage: %v", got)
	}
	if info.EnergyStorage != 0 {
		t.Errorf("EnergyStorage field: %d", info.EnergyStorage)
	}
	if got := info.EffectiveMetalStorage(); got != 1000 {
		t.Errorf("metal storage 1e3: %v", got)
	}
	if info.MetalStorage != 1 {
		t.Errorf("MetalStorage field reads 1e3 like Atol: %d", info.MetalStorage)
	}
	if got := info.EffectiveCruiseAlt(); got != 4464 {
		t.Errorf("cruise alt: %d", got)
	}
	if got := info.EffectiveMakesMetal(); got != 1 {
		t.Errorf("makes metal: %d", got)
	}
	out := marshal(t, u)
	if !strings.Contains(out, "EnergyStorage=0.5;") || !strings.Contains(out, "MetalStorage=1e3;") {
		t.Errorf("fractions lost:\n%s", out)
	}
	info.SetEnergyStorage(2.25)
	if info.EnergyStorage != 2 || info.EffectiveEnergyStorage() != 2.25 {
		t.Errorf("after SetEnergyStorage: %d %v", info.EnergyStorage, info.EffectiveEnergyStorage())
	}
	if out := marshal(t, u); !strings.Contains(out, "energystorage=2.25;") && !strings.Contains(out, "EnergyStorage=2.25;") {
		t.Errorf("SetEnergyStorage not written:\n%s", out)
	}
	// A value changed through the int field wins over the old text.
	info.MetalStorage = 7
	if got := info.EffectiveMetalStorage(); got != 7 {
		t.Errorf("changed metal storage: %v", got)
	}
}

const moveInfo = `
[CLASS0] { Name=TANKSH2; FootprintX=2; FootprintZ=2; MaxWaterDepth=15; MaxSlope=15; }
[CLASS1] { Name=TANKHOVER3; FootprintX=3; FootprintZ=3; MaxSlope=30; MaxWaterSlope=20; }
[CLASS32] { Name=HUGE; FootprintX=9; }
[class1] { Name=SHADOWED; }
[CLASS2] { Name=tanksh2; FootprintX=7; }
[CLASS3] { FootprintX=1; }
`

func decodeMoveInfo(t *testing.T) []MovementClass {
	t.Helper()
	var classes []MovementClass
	if err := tdf.Unmarshal([]byte(moveInfo), &classes); err != nil {
		t.Fatal(err)
	}
	return classes
}

// A unit naming a movement class takes all of the class's values, and the
// game's defaults for the keys the class leaves out, whatever its own keys
// say; the retail hovercraft set MaxWaterDepth=0 and rely on this.
func TestMovementClassReplacesTheUnitsOwnKeys(t *testing.T) {
	classes := decodeMoveInfo(t)
	u := decodeUnit(t, `[UNITINFO]{MovementClass=TANKHOVER3;MaxWaterDepth=0;FootprintX=5;FootprintZ=5;MaxSlope=90;}`)
	l, slot := u.Info.Movement(classes)
	want := MovementLimits{FootprintX: 3, FootprintZ: 3, MaxWaterDepth: 10000, MinWaterDepth: -10000,
		MaxSlope: 20, BadSlope: 15, MaxWaterSlope: 20, BadWaterSlope: 10}
	if slot != 1 || l != want {
		t.Errorf("got %+v slot %d, want %+v slot 1", l, slot, want)
	}
}

func TestMovementClassLookupFollowsTheGame(t *testing.T) {
	classes := decodeMoveInfo(t)
	// Names compare ignoring case and the lowest slot wins.
	if c, slot := FindMovementClass(classes, "TANKSH2"); c == nil || slot != 0 {
		t.Errorf("TANKSH2: slot %d", slot)
	}
	// Only CLASS0..CLASS31 are read, the first section of each name.
	if c, _ := FindMovementClass(classes, "HUGE"); c != nil {
		t.Error("CLASS32 was found")
	}
	if c, _ := FindMovementClass(classes, "SHADOWED"); c != nil {
		t.Error("a second [CLASS1] was found")
	}
	// A unit's empty MovementClass value names a class with no name key.
	if c, slot := FindMovementClass(classes, ""); c == nil || slot != 3 {
		t.Errorf("empty name: slot %d", slot)
	}
	// An unknown class leaves the unit on its own keys.
	u := decodeUnit(t, `[UNITINFO]{MovementClass=NOPE;FootprintX=4;}`)
	if l, slot := u.Info.Movement(classes); slot != -1 || l.FootprintX != 4 || l.MaxWaterDepth != 10000 {
		t.Errorf("unknown class: %+v slot %d", l, slot)
	}
	w := CheckMoveInfo(classes)
	var text []string
	for _, x := range w {
		text = append(text, x.String())
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"[CLASS32]", "[class1]", "CLASS0, which units find first"} {
		if !strings.Contains(joined, want) {
			t.Errorf("CheckMoveInfo missing %q in:\n%s", want, joined)
		}
	}
}

func TestMovementClassLimitsClampSlopes(t *testing.T) {
	var classes []MovementClass
	if err := tdf.Unmarshal([]byte(`[CLASS0]{Name=A;MaxSlope=300;BadSlope=200;MaxWaterDepth=40000;}`), &classes); err != nil {
		t.Fatal(err)
	}
	l := classes[0].Limits()
	// 300 keeps 8 bits (44); the explicit bad slope 200 is capped by it;
	// 40000 keeps 16 bits (-25536).
	if l.MaxSlope != 44 || l.BadSlope != 44 || l.MaxWaterDepth != -25536 {
		t.Errorf("got %+v", l)
	}
}

func TestUnitReadsTheFirstUnitInfoAndKeepsTheSecond(t *testing.T) {
	src := "[UNITINFO]{MaxDamage=100;}\n[UNITINFO]{MaxDamage=900;}\n[EXTRA]{x=1;}\n"
	u := decodeUnit(t, src)
	if u.Info.MaxDamage != 100 {
		t.Errorf("MaxDamage %d, want the first section's 100", u.Info.MaxDamage)
	}
	if len(u.Sections) != 2 {
		t.Fatalf("sections kept: %+v", u.Sections)
	}
	out := marshal(t, u)
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("round trip differs: %s\n%s", msg, out)
	}
	if w := u.Check(); len(w) != 1 || !strings.Contains(w[0].Message, "second [UNITINFO]") {
		t.Errorf("warnings: %v", w)
	}
}

// The legacy Document API and the struct codec read the same text the same
// way.
func TestDocumentAndStructAgree(t *testing.T) {
	src := "[UNITINFO]\n{\n\tName=Immolator; //c\n\tBuildCostMetal=321;  /* C llt=268 */\n\tMaxDamage=842;\n}\n"
	doc, err := tdf.ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	u := decodeUnit(t, src)
	sec := doc.Section("UNITINFO")
	if sec.String("Name") != u.Info.Name || sec.Int("MaxDamage") != u.Info.MaxDamage ||
		float64(sec.Int("BuildCostMetal")) != u.Info.BuildCostMetal {
		t.Errorf("document %q %d %d vs struct %q %d %v", sec.String("Name"), sec.Int("MaxDamage"),
			sec.Int("BuildCostMetal"), u.Info.Name, u.Info.MaxDamage, u.Info.BuildCostMetal)
	}
	if u.Info.Name != "Immolator" || u.Info.BuildCostMetal != 321 {
		t.Errorf("struct: %q %v", u.Info.Name, u.Info.BuildCostMetal)
	}
}

func TestUnitCheckReportsLongText(t *testing.T) {
	u := &Unit{}
	u.Info.UnitName = strings.Repeat("A", 40)
	u.Info.Weapon1 = strings.Repeat("W", 32)
	w := u.Check()
	if len(w) != 2 {
		t.Fatalf("warnings: %v", w)
	}
}
