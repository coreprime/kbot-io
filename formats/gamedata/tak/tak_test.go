package tak

import (
	"errors"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/tdf"
)

func marshal(t *testing.T, v any) string {
	t.Helper()
	out, err := tdf.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func TestUnitKeepsExplicitZerosAndExtraSections(t *testing.T) {
	src := `[UNITINFO]
{
	UnitName=AraMage;
	StandingUnitOrder=0;
	MaxWaterDepth=0;
	[AdjustJoy]{Adjustment=0;Radius=0;}
	[Custom]{x=1;}
}
[WEAPON1]{Name=Bolt;Range=0;[DAMAGE]{default=0;}}
[EXTRA]{y=2;}`
	var u Unit
	if err := tdf.Unmarshal([]byte(src), &u); err != nil {
		t.Fatal(err)
	}
	if len(u.Info.Sections) != 1 || len(u.Sections) != 1 {
		t.Errorf("sections kept: %d nested, %d top-level", len(u.Info.Sections), len(u.Sections))
	}
	out := marshal(t, &u)
	for _, want := range []string{"StandingUnitOrder=0;", "MaxWaterDepth=0;", "Adjustment=0;", "Range=0;", "default=0;", "[Custom]", "[EXTRA]"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q:\n%s", want, out)
		}
	}
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("round trip: %s", msg)
	}
}

// A value a custom type refuses keeps its text; once a tool sets the field,
// only the new value is written.
func TestRefusedValueIsReplacedWhenSet(t *testing.T) {
	var u Unit
	if err := tdf.Unmarshal([]byte("[UNITINFO]{bloodcolor1=red;}"), &u); err != nil {
		t.Fatal(err)
	}
	if out := marshal(t, &u); !strings.Contains(out, "bloodcolor1=red;") {
		t.Errorf("refused text lost:\n%s", out)
	}
	u.Info.BloodColor1 = &common.RGBString{R: 1, G: 2, B: 3}
	out := marshal(t, &u)
	if strings.Count(strings.ToLower(out), "bloodcolor1=") != 1 || !strings.Contains(out, "bloodcolor1=1 2 3;") {
		t.Errorf("after setting:\n%s", out)
	}
}

func TestMapKeepsOddlyNamedChildren(t *testing.T) {
	src := `[GlobalHeader]
{
	kingdom=Aramon;
	[Map Data]
	{
		type=skirmish;
		[specials]{[startpos1]{specialwhat=StartPos1;XPos=0;}[special1]{specialwhat=StartPos2;}}
		[units]{[commander]{unitname=AraMage;healthpercentage=0;}}
	}
}`
	m, err := ReadMap([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	md := m.Header.MapData
	if len(md.Specials.Items) != 2 || md.Specials.Items[0].Key != "startpos1" || len(md.Units.Items) != 1 {
		t.Fatalf("items: %+v %+v", md.Specials.Items, md.Units.Items)
	}
	out := marshal(t, m)
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("round trip: %s\n%s", msg, out)
	}
	if !strings.Contains(out, "[Map Data]") || !strings.Contains(out, "healthpercentage=0;") {
		t.Errorf("output:\n%s", out)
	}
	if w := m.Check(); len(w) != 0 {
		t.Errorf("warnings for a TA: Kingdoms map: %v", w)
	}
	p := md.Units.Add(Placement{UnitName: "X"})
	if p.Key != "unit1" {
		t.Errorf("Add named %q", p.Key)
	}
}

func TestMapReportsATotalAnnihilationFile(t *testing.T) {
	if _, err := ReadMap([]byte("[Other]{}")); !errors.Is(err, ErrNoGlobalHeader) {
		t.Errorf("err: %v", err)
	}
	src := "[GlobalHeader]{[Schema 0]{Type=Network 1;}}"
	m, err := ReadMap([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Header.Sections) != 1 {
		t.Errorf("[Schema 0] not kept: %+v", m.Header.Sections)
	}
	joined := ""
	for _, w := range m.Check() {
		joined += w.String() + "\n"
	}
	if !strings.Contains(joined, "Total Annihilation") || !strings.Contains(joined, "Map Data") {
		t.Errorf("Check:\n%s", joined)
	}
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(marshal(t, m))); !ok {
		t.Errorf("round trip: %s", msg)
	}
}
