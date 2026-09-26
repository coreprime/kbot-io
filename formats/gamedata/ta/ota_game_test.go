package ta

import (
	"errors"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

func readMap(t *testing.T, src string) *Map {
	t.Helper()
	m, err := ReadMap([]byte(src))
	if err != nil {
		t.Fatalf("ReadMap: %v", err)
	}
	return m
}

func roundTrips(t *testing.T, src string, v any) string {
	t.Helper()
	out := marshal(t, v)
	if ok, msg := tdf.SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("round trip differs: %s\n%s", msg, out)
	}
	return out
}

const scenario = `[GLOBALHEADER]
{
	missionname=Scenario;
	numplayers=2 3 4;
	tidalstrength=18.5;
	killmul=1.5;
	timemul=0;
	maxunits=0;
	SCHEMACOUNT=3;
	useonlyunits=only;
	KillEnemyCommander=1;
	Foo=1;
	FOO=2;
	[Schema 0]
	{
		Type=Easy;
		SurfaceMetal=3;
		MeteorWeapon=METEOR;
		MeteorRadius=100;
		MeteorDensity=0.5;
		MeteorDuration=2.5;
		MeteorInterval=0.5;
		[specials]
		{
			[special0] { specialwhat=StartPos1; XPos=100; ZPos=200; }
			[startpos1] { specialwhat=StartPos2; XPos=0; ZPos=0; }
		}
		[units]
		{
			[unit0] { Unitname=ARMCOM; XPos=1e3; HealthPercentage=0; InitialGroup=17; BuildPriority=CORHRK; Player=0; }
			[unit1] { Unitname=ARMPW; HealthPercentage=50%; InitialGroup=patrol; BuildPriority=3; }
		}
		[units] { [unit0] { Unitname=IGNORED; } }
		[features]
		{
			[feature0] { Featurename=Rock; XPos=0; ZPos=5; }
			[feature1] { Featurename=Tree; ZPos=5; }
		}
	}
	[schema 1]
	{
		Type=Network 1;
		[specials] { [a] { specialwhat=StartPos1; XPos=1; ZPos=1; } }
	}
	[Schema 3] { Type=Network 1; }
	[Schema0] { Type=Network 1; }
}`

// Decoding and re-marshalling an OTA keeps everything the game reads:
// explicit zeros, fractions, keys and sections the structs do not model,
// oddly named and duplicate sections, and the header's spelling.
func TestMapRoundTripIsLossless(t *testing.T) {
	m := readMap(t, scenario)
	out := roundTrips(t, scenario, m)
	for _, want := range []string{"[GLOBALHEADER]", "numplayers=2 3 4;", "tidalstrength=18.5;", "killmul=1.5;",
		"maxunits=0;", "SCHEMACOUNT=3;", "MeteorInterval=0.5;", "XPos=1e3;", "HealthPercentage=0;",
		"[startpos1]", "Unitname=IGNORED;", "[Schema0]", "[Schema 3]", "KillEnemyCommander=1;", "XPos=0;"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lost %q", want)
		}
	}
	// Case variants of a key merge into one holding the last value, which is
	// written once.
	if strings.Count(strings.ToLower(out), "foo=") != 1 || !strings.Contains(out, "Foo=2;") {
		t.Errorf("Foo/FOO:\n%s", out)
	}
}

func TestMapGameView(t *testing.T) {
	m := readMap(t, scenario)
	h := &m.Header
	if got := len(h.GameSchemas()); got != 2 {
		t.Fatalf("game schemas: %d", got)
	}
	if h.Schema(1) == nil || h.Schema(1).Type != "Network 1" {
		t.Errorf("Schema(1): %+v", h.Schema(1))
	}
	if got := strings.Join(h.UnreachableSchemas(), ","); got != "Schema 3,Schema0" {
		t.Errorf("unreachable: %q", got)
	}
	if h.NumPlayersText() != "2 3 4" || len(h.NumPlayers) != 1 {
		t.Errorf("numplayers text %q, field %v", h.NumPlayersText(), h.NumPlayers)
	}
	if got := h.PlayerCounts(); len(got) != 3 {
		t.Errorf("player counts: %v", got)
	}
	if h.EffectiveTidalStrength() != 18.5 || h.EffectiveKillMul() != 1.5 || h.EffectiveTimeMul() != 0 {
		t.Errorf("fractions: %v %v %v", h.EffectiveTidalStrength(), h.EffectiveKillMul(), h.EffectiveTimeMul())
	}
	if h.EffectiveMaxUnits() != 0 || h.EffectiveMissionDescription() != DefaultMissionDescription {
		t.Errorf("max units %d, description %q", h.EffectiveMaxUnits(), h.EffectiveMissionDescription())
	}
	s0 := h.Schema(0)
	if s0.EffectiveMeteorDuration() != 2.5 || s0.EffectiveMeteorInterval() != 0.5 || s0.MeteorInterval != 0 {
		t.Errorf("meteors: %v %v", s0.EffectiveMeteorDuration(), s0.EffectiveMeteorInterval())
	}
	starts := s0.StartPositions()
	if len(starts) != 2 || starts[1].Special.Key != "startpos1" || starts[1].Slot != 1 {
		t.Errorf("starts: %+v", starts)
	}
	units := s0.Units.Items
	if units[0].XPos != 1 || units[0].EffectiveHealthPercentage() != 0 || units[1].EffectiveHealthPercentage() != 50 {
		t.Errorf("units: xpos %d health %d %d", units[0].XPos, units[0].EffectiveHealthPercentage(), units[1].EffectiveHealthPercentage())
	}
	if units[0].InitialGroupNumber() != 1 || units[1].InitialGroupNumber() != 0 ||
		units[0].BuildPriorityNumber() != 0 || units[1].BuildPriorityNumber() != 3 || units[0].EffectivePlayer() != 1 {
		t.Errorf("group/priority/player: %d %d %d %d %d", units[0].InitialGroupNumber(), units[1].InitialGroupNumber(),
			units[0].BuildPriorityNumber(), units[1].BuildPriorityNumber(), units[0].EffectivePlayer())
	}
	if fs := s0.GameFeatures(); len(fs) != 1 || fs[0].Name != "Rock" || fs[0].X != 0 {
		t.Errorf("features: %+v", fs)
	}
	if got := h.MultiplayerSchema(2); got != h.Schema(1) {
		t.Errorf("multiplayer schema: %+v", got)
	}
	if got := h.CampaignSchema(1); got != s0 {
		t.Errorf("campaign schema for medium falls back to easy: %+v", got)
	}
	var msgs []string
	for _, w := range m.Check() {
		msgs = append(msgs, w.String())
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"Schema 3", "Schema0", "second [units]", "the game skips the feature"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Check misses %q:\n%s", want, joined)
		}
	}
}

func TestMultiplayerSchemaSelection(t *testing.T) {
	m := readMap(t, `[GlobalHeader]{
		[Schema 0]{Type=Network 1;[specials]{[a]{specialwhat=StartPos1;}[b]{specialwhat=StartPos2;}}}
		[Schema 1]{Type=Network 2;[specials]{[a]{specialwhat=StartPos1;}[b]{specialwhat=StartPos2;}[c]{specialwhat=StartPos3;}[d]{specialwhat=StartPos4;}}}
		[Schema 2]{Type=Network 1;}
		[Schema 3]{Type=Easy;[specials]{[a]{specialwhat=StartPos1;}[b]{specialwhat=StartPos2;}[c]{specialwhat=StartPos3;}}}
	}`)
	h := &m.Header
	for players, want := range map[int]string{0: "Schema 1", 2: "Schema 0", 3: "Schema 1", 4: "Schema 1", 8: "Schema 1"} {
		if got := h.MultiplayerSchema(players); got == nil || got.Key != want {
			t.Errorf("%d players: %+v, want %s", players, got, want)
		}
	}
	if s := h.CampaignSchema(2); s == nil || s.Key != "Schema 3" {
		t.Errorf("campaign hard falls back to easy: %+v", s)
	}
}

func TestStartPositionNumbering(t *testing.T) {
	m := readMap(t, `[GlobalHeader]{[Schema 0]{Type=Network 1;[specials]{
		[a]{specialwhat=StartPos3;XPos=40000;ZPos=-5;}
		[b]{specialwhat=StartPos;}
		[c]{specialwhat=StartPos0;}
		[d]{specialwhat=startpos x;}
		[e]{specialwhat=Other;}
	}}}`)
	got := m.Header.Schema(0).StartPositions()
	want := []struct{ number, slot, x, z int }{{3, 2, -25536, -5}, {1, 0, 0, 0}, {0, 0, 0, 0}, {2, 1, 0, 0}}
	if len(got) != len(want) {
		t.Fatalf("starts: %+v", got)
	}
	for i, w := range want {
		if got[i].Number != w.number || got[i].Slot != w.slot || got[i].X != w.x || got[i].Z != w.z {
			t.Errorf("start %d: %+v, want %+v", i, got[i], w)
		}
	}
	var sharing int
	for _, w := range m.Check() {
		if strings.Contains(w.Message, "player slot 0") || strings.Contains(w.Message, "outside 16 bits") {
			sharing++
		}
	}
	if sharing != 2 {
		t.Errorf("Check: %v", m.Check())
	}
}

// New schemas are named "Schema N" with the space the game looks for.
func TestNewSchemasAreNamedWithASpace(t *testing.T) {
	var m Map
	m.Header.Schemas = append(m.Header.Schemas, Schema{Type: "Network 1"})
	m.Header.AddSchema(Schema{Type: "Network 2"})
	out := marshal(t, &m)
	if !strings.Contains(out, "[Schema 0]") || !strings.Contains(out, "[Schema 1]") || strings.Contains(out, "[Schema0]") {
		t.Errorf("schema names:\n%s", out)
	}
	// A map built in code carries SCHEMACOUNT, as retail maps do.
	if !strings.Contains(out, "[GlobalHeader]") || !strings.Contains(out, "SCHEMACOUNT=2;") {
		t.Errorf("header:\n%s", out)
	}
	back := readMap(t, out)
	if len(back.Header.GameSchemas()) != 2 {
		t.Errorf("the game finds %d schemas", len(back.Header.GameSchemas()))
	}
	if again := marshal(t, back); strings.Count(again, "SCHEMACOUNT") != 1 {
		t.Errorf("SCHEMACOUNT after a round trip:\n%s", again)
	}
	m2 := readMap(t, "[GlobalHeader]{SCHEMACOUNT=1;[Schema 0]{Type=Easy;}}")
	m2.Header.AddSchema(Schema{})
	if out := marshal(t, m2); !strings.Contains(out, "SCHEMACOUNT=2;") || !strings.Contains(out, "[Schema 1]") {
		t.Errorf("AddSchema:\n%s", out)
	}
}

// A decoded header keeps SCHEMACOUNT as written, or keeps it absent, until a
// schema helper changes the schemas.
func TestDecodedSchemaCountIsKept(t *testing.T) {
	wrong := readMap(t, "[GlobalHeader]{schemacount=5;[Schema 0]{Type=Easy;}}")
	if out := marshal(t, wrong); !strings.Contains(out, "schemacount=5;") || strings.Count(strings.ToLower(out), "schemacount") != 1 {
		t.Errorf("wrong count not kept:\n%s", out)
	}
	none := readMap(t, "[GlobalHeader]{[Schema 0]{Type=Easy;}}")
	if out := marshal(t, none); strings.Contains(strings.ToLower(out), "schemacount") {
		t.Errorf("count added to a map without one:\n%s", out)
	}
	none.Header.AddSchema(Schema{Type: "Hard"})
	if out := marshal(t, none); !strings.Contains(out, "SCHEMACOUNT=2;") {
		t.Errorf("AddSchema on a map without a count:\n%s", out)
	}
}

// An unnamed schema takes a number no other schema has, and the game view
// before writing matches the file written.
func TestUnnamedSchemasTakeFreeNumbers(t *testing.T) {
	var m Map
	m.Header.Schemas = []Schema{{Key: "Schema 1", Type: "Network 2"}, {Type: "Network 1"}}
	h := &m.Header
	got := h.GameSchemas()
	if len(got) != 2 || got[0].Type != "Network 1" || got[1].Type != "Network 2" {
		t.Fatalf("game view before writing: %+v", got)
	}
	out := marshal(t, &m)
	if strings.Count(out, "[Schema 1]") != 1 || strings.Count(out, "[Schema 0]") != 1 {
		t.Errorf("names:\n%s", out)
	}
	back := readMap(t, out).Header.GameSchemas()
	if len(back) != 2 || back[0].Type != "Network 1" || back[1].Type != "Network 2" {
		t.Errorf("game view after reading back: %+v", back)
	}
	if s := h.AddSchema(Schema{}); s.Key != "Schema 2" {
		t.Errorf("AddSchema named %q", s.Key)
	}
}

func TestRemoveAndRenumberSchemas(t *testing.T) {
	const src = "[GlobalHeader]{SCHEMACOUNT=3;[Schema 0]{Type=A;}[Schema 1]{Type=B;}[Schema 2]{Type=C;}}"
	types := func(h *GlobalHeader) string {
		var parts []string
		for _, s := range h.GameSchemas() {
			parts = append(parts, s.Type)
		}
		return strings.Join(parts, ",")
	}

	// Deleting from the slice leaves a gap the game stops at.
	m := readMap(t, src)
	m.Header.Schemas = m.Header.Schemas[1:]
	if got := types(&m.Header); got != "" {
		t.Errorf("after deleting Schema 0 the game finds %q", got)
	}
	m.Header.RenumberSchemas()
	out := marshal(t, m)
	if got := types(&readMap(t, out).Header); got != "B,C" || !strings.Contains(out, "SCHEMACOUNT=2;") {
		t.Errorf("after RenumberSchemas the game finds %q:\n%s", got, out)
	}

	m = readMap(t, src)
	if !m.Header.RemoveSchema(1) || m.Header.RemoveSchema(7) {
		t.Error("RemoveSchema result")
	}
	if got := types(&m.Header); got != "A,C" || m.Header.Remaining["SCHEMACOUNT"] != "2" {
		t.Errorf("after RemoveSchema(1): %q, count %q", got, m.Header.Remaining["SCHEMACOUNT"])
	}

	// Renumbering keeps the numbers the game already reads and puts the
	// schemas it cannot reach after them.
	m = readMap(t, "[GlobalHeader]{[Schema 1]{Type=B;}[Schema 00]{Type=X;}[Schema 0]{Type=A;}[schema 1]{Type=Y;}[Schema 4]{Type=C;}}")
	m.Header.RenumberSchemas()
	if got := types(&m.Header); got != "A,B,C,X,Y" {
		t.Errorf("renumbered order %q", got)
	}
	for i, s := range m.Header.Schemas {
		if s.Key != "Schema "+string(rune('0'+i)) {
			t.Errorf("Schemas[%d] named %q", i, s.Key)
		}
	}
	if u := m.Header.UnreachableSchemas(); len(u) != 0 {
		t.Errorf("unreachable after renumbering: %v", u)
	}
}

// Placed entries built in code without a name are written with one, never as
// an unnamed [] section, and read back unchanged.
func TestUnnamedPlacementsAreNamed(t *testing.T) {
	var m Map
	m.Header.Schemas = []Schema{{
		Type:     "Network 1",
		Specials: &Specials{Items: []Special{{SpecialWhat: "StartPos1", XPos: 5}, {Key: "special0", SpecialWhat: "StartPos2"}}},
		Units:    &Units{Items: []Special{{UnitName: "ARMCOM"}}},
		Features: &Features{Items: []Special{{FeatureName: "Rock", XPos: 1, ZPos: 2}}},
	}}
	out := marshal(t, &m)
	if strings.Contains(out, "[]") {
		t.Fatalf("unnamed section written:\n%s", out)
	}
	for _, want := range []string{"[special1]", "[special0]", "[unit0]", "[feature0]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
	back := readMap(t, out)
	s := back.Header.Schema(0)
	if s == nil || len(s.Specials.Items) != 2 || s.Specials.Items[0].Key != "special1" ||
		s.Specials.Items[0].XPos != 5 || s.Units.Items[0].UnitName != "ARMCOM" || len(s.GameFeatures()) != 1 {
		t.Fatalf("read back: %+v", s)
	}
	if again := marshal(t, back); again != out {
		t.Errorf("second write differs:\n%s\n---\n%s", out, again)
	}
	if p := s.Specials.Add(Special{SpecialWhat: "StartPos3"}); p.Key != "special2" {
		t.Errorf("Add named %q", p.Key)
	}
}

func TestMapSettersWriteFractionsAndText(t *testing.T) {
	m := readMap(t, "[GlobalHeader]{numplayers=2, 3;tidalstrength=20;[Schema 0]{MeteorInterval=5;}}")
	h := &m.Header
	h.SetTidalStrength(18.25)
	h.SetKillMul(0.5)
	h.SetNumPlayersText("2-8")
	h.Schema(0).SetMeteorInterval(0.75)
	if h.TidalStrength != 18 || h.EffectiveTidalStrength() != 18.25 || h.NumPlayersText() != "2-8" {
		t.Errorf("after setters: %d %v %q", h.TidalStrength, h.EffectiveTidalStrength(), h.NumPlayersText())
	}
	out := marshal(t, m)
	for _, want := range []string{"tidalstrength=18.25;", "killmul=0.5;", "numplayers=2-8;", "meteorinterval=0.75;"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q:\n%s", want, out)
		}
	}
	back := readMap(t, out)
	if back.Header.EffectiveTidalStrength() != 18.25 || len(back.Header.PlayerCounts()) != 7 {
		t.Errorf("read back: %v %v", back.Header.EffectiveTidalStrength(), back.Header.PlayerCounts())
	}
}

func TestMapWriterRefusesTextTheGameReadsBack(t *testing.T) {
	var m Map
	m.Header.MissionDescription = "More maps at http://example.com"
	if _, err := tdf.Marshal(&m); err == nil {
		t.Error("a // in a value was written")
	}
	m.Header.MissionDescription = "a; b"
	if _, err := tdf.Marshal(&m); err == nil {
		t.Error("a ; in a value was written")
	}
}

func TestReadMapNeedsAGlobalHeader(t *testing.T) {
	if _, err := ReadMap([]byte("[Other]{x=1;}")); !errors.Is(err, ErrNoGlobalHeader) {
		t.Errorf("err: %v", err)
	}
	// A TA: Kingdoms map keeps its [Map Data] and Check points it out.
	m := readMap(t, "[GlobalHeader]{kingdom=Aramon;[Map Data]{type=skirmish;}}")
	if len(m.Header.Sections) != 1 {
		t.Fatalf("[Map Data] not kept: %+v", m.Header.Sections)
	}
	found := false
	for _, w := range m.Check() {
		if strings.Contains(w.Message, "TA: Kingdoms") {
			found = true
		}
	}
	if !found {
		t.Errorf("Check: %v", m.Check())
	}
}

// Numbers read like atol, the same on every platform.
func TestMapNumbersReadLikeTheGame(t *testing.T) {
	m := readMap(t, "[GlobalHeader]{[Schema 0]{[specials]{[a]{XPos=inf;ZPos=1e30;}[b]{XPos=-7x;ZPos=4294967397;}}}}")
	items := m.Header.Schema(0).Specials.Items
	if items[0].XPos != 0 || items[0].ZPos != 1 || items[1].XPos != -7 || items[1].ZPos != 101 {
		t.Errorf("got %+v %+v", items[0], items[1])
	}
}

func TestStrictReadingRefusesATruncatedMap(t *testing.T) {
	var m Map
	err := tdf.UnmarshalWith([]byte("[GlobalHeader]{[Schema 0]{ZPos=20}"), &m, tdf.ParseOptions{Strict: true})
	if err == nil {
		t.Error("strict reading accepted a truncated map")
	}
}

func TestMeteorsFallBackToTheDefaults(t *testing.T) {
	var defaults []Meteor
	if err := tdf.Unmarshal([]byte("[Default]{MeteorWeapon=METEOR;MeteorRadius=100;MeteorDensity=1.5;MeteorDuration=30;MeteorInterval=0.5;}"), &defaults); err != nil {
		t.Fatal(err)
	}
	d := &defaults[0]
	if d.EffectiveMeteorInterval() != 0.5 {
		t.Errorf("default interval: %v", d.EffectiveMeteorInterval())
	}
	m := readMap(t, `[GlobalHeader]{
		[Schema 0]{MeteorWeapon=ROCK;MeteorRadius=5;MeteorDensity=2;MeteorDuration=3;MeteorInterval=0.25;}
		[Schema 1]{MeteorWeapon=ROCK;MeteorRadius=5;MeteorDensity=2;MeteorDuration=3;}
		[Schema 2]{}
	}`)
	h := &m.Header
	if s := h.Schema(0).Meteors(d); !s.Enabled || s.Weapon != "ROCK" || s.Interval != 0.25 {
		t.Errorf("own settings: %+v", s)
	}
	if s := h.Schema(1).Meteors(d); !s.Enabled || s.Weapon != "METEOR" || s.Interval != 0.5 {
		t.Errorf("fallback: %+v", s)
	}
	if s := h.Schema(2).Meteors(d); s.Enabled {
		t.Errorf("no weapon: %+v", s)
	}
}
