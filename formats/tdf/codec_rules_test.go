package tdf

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// anySection is a generic section: name, fields and child sections.
type anySection struct {
	Key      string            `tdf:",name"`
	Values   map[string]string `tdf:",remaining"`
	Children []anySection      `tdf:",sections"`
}

func TestCodecIntegersReadLikeAtol(t *testing.T) {
	type rec struct {
		A int    `tdf:"a"`
		B int    `tdf:"b"`
		C int32  `tdf:"c"`
		D uint8  `tdf:"d"`
		E int16  `tdf:"e"`
		F uint32 `tdf:"f"`
		G int    `tdf:"g"`
		H *int   `tdf:"h"`
	}
	var r rec
	src := "a=12abc; b=1e3; c=4294967297; d=-1; e=70000; f=3000000000; g=1e30; h=0x10;"
	if err := Unmarshal([]byte(src), &r); err != nil {
		t.Fatal(err)
	}
	if r.A != 12 || r.B != 1 || r.C != 1 || r.D != 255 || r.E != 4464 || r.F != 3000000000 || r.G != 1 ||
		r.H == nil || *r.H != 0 {
		t.Errorf("got %+v (h=%v)", r, r.H)
	}
}

func TestCodecFloatsAndFlagsReadLikeTheGame(t *testing.T) {
	type rec struct {
		A float64           `tdf:"a"`
		B float64           `tdf:"b"`
		C float32           `tdf:"c"`
		D bool              `tdf:"d"`
		E bool              `tdf:"e"`
		F bool              `tdf:"f"`
		G float64           `tdf:"g"`
		X map[string]string `tdf:",remaining"`
	}
	var r rec
	if err := Unmarshal([]byte("a=13O; b=1.5d2; c=.5; d=2; e=3; f=true; g=inf;"), &r); err != nil {
		t.Fatal(err)
	}
	if r.A != 13 || r.B != 150 || r.C != 0.5 || r.D || !r.E || r.F || r.G != 0 {
		t.Errorf("got %+v", r)
	}
	// Text the typed value would not reproduce stays in the catch-all.
	for _, k := range []string{"a", "b", "d", "f", "g"} {
		if _, ok := r.X[k]; !ok {
			t.Errorf("catch-all is missing the text of %s: %v", k, r.X)
		}
	}
	if _, ok := r.X["c"]; ok {
		t.Errorf(".5 re-renders as 0.5, which reads the same; it needs no catch-all copy: %v", r.X)
	}
}

func TestCodecKeepsDirtyTextOnRoundTrip(t *testing.T) {
	type weapon struct {
		Name  string            `tdf:",name"`
		Accel float64           `tdf:"weaponacceleration,omitempty"`
		Flag  int               `tdf:"canmove,omitempty"`
		Extra map[string]string `tdf:",remaining"`
	}
	src := "[W]{weaponacceleration=13O; canmove=2;}"
	var ws []weapon
	if err := Unmarshal([]byte(src), &ws); err != nil {
		t.Fatal(err)
	}
	if ws[0].Accel != 13 {
		t.Errorf("typed value = %v, want the game's 13", ws[0].Accel)
	}
	out, err := Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "weaponacceleration") != 1 || !strings.Contains(string(out), "weaponacceleration=13O;") {
		t.Errorf("dirty text must be written once, as read:\n%s", out)
	}
	if ok, msg := SemanticEqual([]byte(src), out); !ok {
		t.Error(msg)
	}
	if got := MisparsedKeys(ws); len(got) != 1 || got[0] != "weapon.weaponacceleration=13O" {
		t.Errorf("MisparsedKeys = %v", got)
	}

	// A changed value replaces the stale text.
	ws[0].Accel = 20
	out, _ = Marshal(ws)
	if strings.Contains(string(out), "13O") || !strings.Contains(string(out), "weaponacceleration=20;") {
		t.Errorf("stale text written:\n%s", out)
	}
}

func TestCodecDamageMapReadsLikeAtol(t *testing.T) {
	type weapon struct {
		Name   string         `tdf:",name"`
		Damage map[string]int `tdf:"damage"`
	}
	var ws []weapon
	if err := Unmarshal([]byte("[W]{[DAMAGE]{default=13O; armcom=12abc;}}"), &ws); err != nil {
		t.Fatalf("a non-numeric DAMAGE value must not fail the file: %v", err)
	}
	if ws[0].Damage["default"] != 13 || ws[0].Damage["armcom"] != 12 {
		t.Errorf("damage = %v", ws[0].Damage)
	}
}

func TestCodecMergesCaseVariants(t *testing.T) {
	type weapon struct {
		Name   string            `tdf:",name"`
		Damage map[string]int    `tdf:"damage"`
		Extra  map[string]string `tdf:",remaining"`
	}
	src := "[W]{Foo=1; FOO=2; [DAMAGE]{armcom=10; ARMCOM=20;}}"
	var ws []weapon
	if err := Unmarshal([]byte(src), &ws); err != nil {
		t.Fatal(err)
	}
	w := ws[0]
	if len(w.Damage) != 1 || w.Damage["armcom"] != 20 {
		t.Errorf("damage = %v, want one entry holding the last value", w.Damage)
	}
	if len(w.Extra) != 1 || w.Extra["Foo"] != "2" {
		t.Errorf("extra = %v, want one entry holding the last value", w.Extra)
	}
	out, err := Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	if ok, msg := SemanticEqual([]byte(src), out); !ok {
		t.Errorf("%s\n%s", msg, out)
	}
}

func TestCodecFirstDuplicateSectionWins(t *testing.T) {
	type info struct {
		Side string `tdf:"side"`
	}
	type unit struct {
		Info   info           `tdf:"UNITINFO"`
		Damage map[string]int `tdf:"damage"`
		Rest   []anySection   `tdf:",sections"`
	}
	var u unit
	src := "[UNITINFO]{Side=ARM;}\n[UNITINFO]{Side=CORE;}\n[DAMAGE]{a=1;}\n[damage]{a=2;}"
	if err := Unmarshal([]byte(src), &u); err != nil {
		t.Fatal(err)
	}
	if u.Info.Side != "ARM" || u.Damage["a"] != 1 {
		t.Errorf("first sections must win: %+v", u)
	}
	if len(u.Rest) != 2 {
		t.Errorf("later duplicates belong in the catch-all, got %d", len(u.Rest))
	}

	type noCatchAll struct {
		Info info `tdf:"UNITINFO"`
	}
	var n noCatchAll
	if err := Unmarshal([]byte("[UNITINFO]{Side=ARM;}[UNITINFO]{Side=CORE;}"), &n); err != nil || n.Info.Side != "ARM" {
		t.Errorf("no merge of later duplicates: %v %+v", err, n)
	}
}

func TestCodecFoldsASCIIOnly(t *testing.T) {
	type rec struct {
		Name  string            `tdf:"café"`
		Extra map[string]string `tdf:",remaining"`
	}
	var r rec
	// "CAFé" differs from the tag in ASCII case only; "cafÉ" differs in a
	// non-ASCII letter, which the game compares exactly.
	if err := Unmarshal([]byte("CAFé=a; cafÉ=b; x\xC9=1; X\xE9=2;"), &r); err != nil {
		t.Fatal(err)
	}
	if r.Name != "a" || r.Extra["cafÉ"] != "b" || r.Extra["x\xC9"] != "1" || r.Extra["X\xE9"] != "2" {
		t.Errorf("got %q", r)
	}
}

func TestMarshalRefusesUnrepresentableText(t *testing.T) {
	type rec struct {
		Name  string            `tdf:",name"`
		S     string            `tdf:"s,omitempty"`
		F     float64           `tdf:"f,omitempty"`
		I     int64             `tdf:"i,omitempty"`
		U     uint64            `tdf:"u,omitempty"`
		Extra map[string]string `tdf:",remaining"`
	}
	for _, r := range []rec{
		{Name: "A", S: "a;b"},
		{Name: "A", S: "See http://tauniverse.com"},
		{Name: "A", S: "x /* y"},
		{Name: "A", S: " lead"},
		{Name: "A", S: "nul\x00"},
		{Name: "A]", S: "x"},
		{Name: "A", Extra: map[string]string{"k=v": "1"}},
		{Name: "A", Extra: map[string]string{"[k": "1"}},
		{Name: "A", F: math.NaN()},
		{Name: "A", I: math.MaxInt32 + 1},
		{Name: "A", U: math.MaxUint32 + 1},
	} {
		if out, err := Marshal([]rec{r}); err == nil {
			t.Errorf("Marshal(%+v) = %q, want an error", r, out)
		}
	}
	var we *WriteError
	if _, err := Marshal([]rec{{Name: "A", S: "a;b"}}); !errors.As(err, &we) || we.What != "value" {
		t.Errorf("want a *WriteError for the value, got %v", err)
	}
	out, err := Marshal([]rec{{Name: "A", S: "a}b{c=d\r\ne", F: math.Inf(1), I: math.MinInt32}})
	if err != nil {
		t.Fatal(err)
	}
	var back []rec
	if err := Unmarshal(out, &back); err != nil || back[0].S != "a}b{c=d\r\ne" || !math.IsInf(back[0].F, 1) || back[0].I != math.MinInt32 {
		t.Errorf("representable text must round-trip: %v %+v\n%s", err, back, out)
	}
}

func TestCheckFunctions(t *testing.T) {
	for _, k := range []string{"UnitName", ";y", "a}b", "", "a b"} {
		if err := CheckKey(k); err != nil {
			t.Errorf("CheckKey(%q) = %v", k, err)
		}
	}
	for _, k := range []string{"a=b", "[a", "}a", "a//b", " a", "a\n"} {
		if CheckKey(k) == nil {
			t.Errorf("CheckKey(%q) accepted", k)
		}
	}
	for _, v := range []string{"", "a b", "1}", "x=y", "line\nbreak", "http:/x"} {
		if err := CheckValue(v); err != nil {
			t.Errorf("CheckValue(%q) = %v", v, err)
		}
	}
	for _, v := range []string{";", "a//", "/*", "\t", "x\x00"} {
		if CheckValue(v) == nil {
			t.Errorf("CheckValue(%q) accepted", v)
		}
	}
	if CheckName("Schema 0") != nil || CheckName("a]") == nil || CheckName("a[b") != nil {
		t.Error("CheckName")
	}
}
