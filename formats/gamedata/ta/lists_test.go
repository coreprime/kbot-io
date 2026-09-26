package ta

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

func sideSource(name string, rects bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n{\n\tname=%s;\n\tnameprefix=%s;\n", name, name, name[:3])
	if rects {
		for _, r := range SideRectangles {
			fmt.Fprintf(&b, "\t[%s]{x1=1;y1=2;x2=3;y2=4;}\n", r)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

func TestGameSidesAreContiguousAndCapped(t *testing.T) {
	src := sideSource("SIDE0", true) + sideSource("SIDE1", true) + sideSource("SIDE3", true) +
		sideSource("SIDE01", true) + "[CANBUILD]{[ARMCOM]{canbuild1=ARMSOLAR;}}\n[CANBUILD]{[X]{canbuild1=Y;}}\n"
	var sd SideData
	if err := tdf.Unmarshal([]byte(src), &sd); err != nil {
		t.Fatal(err)
	}
	sides := sd.GameSides()
	if len(sides) != 2 || sides[0].Key != "SIDE0" || sides[1].Key != "SIDE1" {
		t.Fatalf("game sides: %d", len(sides))
	}
	if len(sd.Sections) != 1 || sd.CanBuild.Builder("armcom") == nil {
		t.Errorf("second [CANBUILD] not kept apart: %+v", sd.Sections)
	}
	out := marshal(t, &sd)
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("round trip: %s", msg)
	}
	w := sd.Check()
	var text []string
	for _, x := range w {
		text = append(text, x.String())
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"[SIDE3]", "[SIDE01]", "second [CANBUILD]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Check misses %q:\n%s", want, joined)
		}
	}

	many := ""
	for i := 0; i < 7; i++ {
		many += sideSource(fmt.Sprintf("SIDE%d", i), true)
	}
	var six []Side
	if err := tdf.Unmarshal([]byte(many), &six); err != nil {
		t.Fatal(err)
	}
	if got := len(GameSides(six)); got != SideLimit {
		t.Errorf("sides read: %d, want %d", got, SideLimit)
	}
}

func TestCheckSidesReportsMissingRectangles(t *testing.T) {
	var sides []Side
	src := sideSource("SIDE0", false) + "\n"
	if err := tdf.Unmarshal([]byte(src), &sides); err != nil {
		t.Fatal(err)
	}
	w := CheckSides(sides)
	if len(w) != len(SideRectangles) || !strings.Contains(w[0].Message, "[LOGO]") {
		t.Errorf("warnings: %v", w)
	}
	sides[0].NamePrefix = "ARMX"
	if w := CheckSides(sides); !strings.Contains(w[len(w)-1].String(), `"ARM"`) {
		t.Errorf("name prefix: %v", w[len(w)-1])
	}
}

func TestBuildListStopsAtTheFirstGapAndCaps(t *testing.T) {
	var sd SideData
	src := "[CANBUILD]{[ARMCOM]{canbuild1=ARMSOLAR;CANBUILD2=;canbuild3=ARMLAB;canbuild5=ARMVP;}}"
	if err := tdf.Unmarshal([]byte(src), &sd); err != nil {
		t.Fatal(err)
	}
	b := sd.CanBuild.Builder("ARMCOM")
	if got := b.BuildList(nil); strings.Join(got, ",") != "ARMSOLAR,ARMLAB" {
		t.Errorf("build list: %v", got)
	}
	known := func(n string) bool { return n != "ARMLAB" }
	if got := b.BuildList(known); strings.Join(got, ",") != "ARMSOLAR" {
		t.Errorf("with known units: %v", got)
	}
	if w := sd.Check(); len(w) != 1 || w[0].Key != "canbuild5" {
		t.Errorf("gap warning: %v", w)
	}

	var long strings.Builder
	long.WriteString("[CANBUILD]{[ARMCOM]{")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&long, "canbuild%d=U%d;", i, i)
	}
	long.WriteString("}}")
	var capped SideData
	if err := tdf.Unmarshal([]byte(long.String()), &capped); err != nil {
		t.Fatal(err)
	}
	if got := capped.CanBuild.Builder("ARMCOM").BuildList(nil); len(got) != CanBuildLimit || got[29] != "U30" {
		t.Errorf("cap: %d", len(got))
	}
}

func TestSoundsStopAtTheFirstGap(t *testing.T) {
	var classes []SoundClass
	src := `[ARM_KBOT]
{
	select1=kbarmsel;
	select1text=Ready;
	select3=kbarmse2;
	ok=base;
	ok1=;
	ok2=kbarmmov;
	[NESTED]{x=1;}
}`
	if err := tdf.Unmarshal([]byte(src), &classes); err != nil {
		t.Fatal(err)
	}
	c := &classes[0]
	sel := c.Sounds("select")
	if len(sel) != 1 || sel[0].Sound != "kbarmsel" || sel[0].Text != "Ready" {
		t.Errorf("select: %+v", sel)
	}
	ok := c.Sounds("OK")
	if len(ok) != 3 || ok[0].Sound != "base" || ok[1].Sound != "" || ok[2].Sound != "kbarmmov" {
		t.Errorf("ok: %+v", ok)
	}
	if len(SoundEvents) != 23 {
		t.Errorf("events: %d", len(SoundEvents))
	}
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(marshal(t, classes))); !ok {
		t.Errorf("nested section lost: %s", msg)
	}
}
