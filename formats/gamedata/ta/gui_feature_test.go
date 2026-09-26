package ta

import (
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

// An all-zero [VERSION] and the help text inside [COMMON] survive, and help
// is read where the game reads it.
func TestGUIKeepsVersionAndCommonHelp(t *testing.T) {
	src := `[GADGET0]
{
	[VERSION]{major=0;minor=0;revision=0;}
	[COMMON]{id=0;name=Panel;help=Click here;gaffile=2;}
	totalgadgets=1;
}`
	var gs []Gadget
	if err := tdf.Unmarshal([]byte(src), &gs); err != nil {
		t.Fatal(err)
	}
	if gs[0].Common.Help != "Click here" || gs[0].Common.GafFile != 2 || gs[0].Help != "" {
		t.Errorf("help %q gaffile %d gadget help %q", gs[0].Common.Help, gs[0].Common.GafFile, gs[0].Help)
	}
	out := marshal(t, gs)
	if !strings.Contains(out, "[VERSION]") || !strings.Contains(out, "major=0;") {
		t.Errorf("[VERSION] lost:\n%s", out)
	}
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("round trip: %s", msg)
	}
}

func TestFeatureDefaultsAndWidths(t *testing.T) {
	var fs []Feature
	src := `[Rock1]{metal=56.8;energy=70000;height=200;}
[Tree]{autoreclaimable=0;description=A very long feature description;}
[rock1]{metal=1;}`
	if err := tdf.Unmarshal([]byte(src), &fs); err != nil {
		t.Fatal(err)
	}
	if !fs[0].EffectiveAutoReclaimable() || fs[1].EffectiveAutoReclaimable() {
		t.Error("autoreclaimable: missing should be true, explicit 0 false")
	}
	if fs[0].EffectiveMetal() != 56 || fs[0].EffectiveEnergy() != 4464 || fs[0].EffectiveHeight() != -56 {
		t.Errorf("metal %d energy %d height %d", fs[0].EffectiveMetal(), fs[0].EffectiveEnergy(), fs[0].EffectiveHeight())
	}
	if !strings.Contains(marshal(t, fs), "autoreclaimable=0;") {
		t.Error("explicit autoreclaimable=0 dropped")
	}
	if w := CheckFeatures(fs); len(w) != 3 {
		t.Errorf("warnings: %v", w)
	}
}
