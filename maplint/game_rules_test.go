package maplint

import (
	"strings"
	"testing"
)

// otaText is an OTA with the schema layout the game-rule tests share:
// Schema 0 is a 2-start Network 1 schema, there is no Schema 1, and Schema 2
// (4 starts) and [Schema3] (no space) are sections the game never reads.
const otaText = `[GlobalHeader]
{
	missionname=Rules;
	numplayers=2, 4;
	[schema 0]
	{
		Type=network 1;
		SurfaceMetal=3;
		[specials]
		{
			[special0] { specialwhat=StartPos1; XPos=100; ZPos=200; }
			[odd] { specialwhat=startpos2; XPos=300; ZPos=400; }
			[special2] { specialwhat=Feature; XPos=1; ZPos=1; }
		}
	}
	[Schema 2]
	{
		Type=Network 1;
		[specials]
		{
			[a] { specialwhat=StartPos1; XPos=1; ZPos=1; }
			[b] { specialwhat=StartPos2; XPos=1; ZPos=1; }
			[c] { specialwhat=StartPos3; XPos=1; ZPos=1; }
			[d] { specialwhat=StartPos4; XPos=1; ZPos=1; }
		}
	}
	[Schema3]
	{
		Type=Network 1;
	}
}
`

func TestParseOTAFollowsTheGamesSchemaRules(t *testing.T) {
	info, err := ParseOTA(otaText)
	if err != nil || info == nil {
		t.Fatalf("ParseOTA: %v, %v", info, err)
	}
	if len(info.Schemas) != 1 || info.Schemas[0].Name != "0" || info.Schemas[0].Type != "network 1" {
		t.Fatalf("schemas: %+v", info.Schemas)
	}
	sp := info.Schemas[0].StartPos
	if len(sp) != 2 || sp[1].Number != 2 || sp[1].Slot != 1 || sp[1].X != 300 {
		t.Fatalf("start positions: %+v", sp)
	}
	if got := strings.Join(info.UnreachableSchemas, ","); got != "Schema 2,Schema3" {
		t.Errorf("unreachable: %q", got)
	}
	if info.NumPlayers != "2, 4" {
		t.Errorf("numplayers text: %q", info.NumPlayers)
	}
}

// With numplayers 2 and 4 the game plays 4 players on the 2-start Schema 0,
// since Schema 2 lies past the gap.
func TestSchemaSlotsFollowTheGamesSelection(t *testing.T) {
	info, err := ParseOTA(otaText)
	if err != nil {
		t.Fatal(err)
	}
	d := CheckSchemaSlotsVsPlayers(Input{OTA: info})
	if d.Severity != SeverityWarning || !strings.Contains(d.Message, "4") || !strings.Contains(d.Message, "Schema 2") {
		t.Fatalf("got %q (%s)", d.Severity, d.Message)
	}
}

func TestParseOTAKeepsStartPosZeroAndUnnumberedStarts(t *testing.T) {
	info, err := ParseOTA(`[GlobalHeader]{[Schema 0]{Type=Network 1;[specials]{
		[s0]{specialwhat=StartPos0;XPos=1;ZPos=2;}
		[s1]{specialwhat=StartPos;XPos=3;ZPos=4;}
		[s2]{specialwhat=startposition;XPos=5;ZPos=6;}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	sp := info.Schemas[0].StartPos
	want := []StartPos{{Number: 0, Slot: 0, X: 1, Z: 2}, {Number: 1, Slot: 0, X: 3, Z: 4}, {Number: 2, Slot: 1, X: 5, Z: 6}}
	if len(sp) != len(want) {
		t.Fatalf("got %+v", sp)
	}
	for i := range want {
		if sp[i] != want[i] {
			t.Errorf("start %d: got %+v want %+v", i, sp[i], want[i])
		}
	}
}

func TestSchemaSlotsNeedANetworkSchema(t *testing.T) {
	ota := &OTAInfo{NumPlayers: "2", Schemas: []SchemaInfo{{Name: "0", Type: "Easy", StartPos: makeStarts(2)}}}
	d := CheckSchemaSlotsVsPlayers(Input{OTA: ota})
	if d.Severity != SeverityWarning || !strings.Contains(d.Message, "Network") {
		t.Fatalf("got %q (%s)", d.Severity, d.Message)
	}
}

func TestMultiplayerSchemaPrefersAnExactCount(t *testing.T) {
	schemas := []SchemaInfo{
		{Type: "Network 1", StartPos: makeStarts(10)},
		{Type: "Network 2", StartPos: makeStarts(3)},
		{Type: "Network 1", StartPos: makeStarts(5)},
	}
	for _, c := range []struct{ players, want int }{{0, 1}, {3, 1}, {5, 2}, {8, 0}, {2, 0}} {
		if got := MultiplayerSchema(schemas, c.players); got != c.want {
			t.Errorf("players %d: got schema %d, want %d", c.players, got, c.want)
		}
	}
}

func TestParseOTAWithoutGlobalHeader(t *testing.T) {
	info, err := ParseOTA("[Other]{x=1;}")
	if err != nil || info != nil {
		t.Fatalf("got %+v, %v", info, err)
	}
}
