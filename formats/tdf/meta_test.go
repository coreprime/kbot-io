package tdf

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

type metaUnit struct {
	Name     string            `tdf:",name"`
	Alpha    int               `tdf:"alpha,omitempty"`
	Mid      float64           `tdf:"mid,omitempty"`
	Zeta     int               `tdf:"zeta,omitempty"`
	Missing  int               `tdf:"missing,omitempty"`
	Object   string            `tdf:"objectname,omitempty"`
	Cats     []string          `tdf:"category,omitempty"`
	Hover    bool              `tdf:"canhover,omitempty"`
	Damage   map[string]int    `tdf:"damage,omitempty"`
	Extra    map[string]string `tdf:",remaining"`
	Sections []anySection      `tdf:",sections"`
	Meta     Meta              `tdf:",meta"`
}

func decodeMetaUnits(t *testing.T, src string) []metaUnit {
	t.Helper()
	var us []metaUnit
	if err := Unmarshal([]byte(src), &us); err != nil {
		t.Fatal(err)
	}
	return us
}

func TestMetaRecordsPresence(t *testing.T) {
	us := decodeMetaUnits(t, "[U]{Zeta=0; objectname=; other=1;}")
	m := &us[0].Meta
	if !m.Present("zeta") || !m.Present("OBJECTNAME") || !m.Present("other") || m.Present("missing") || m.Present("alpha") {
		t.Errorf("presence: %v", m.Keys())
	}
	if raw, ok := m.Raw("zeta"); !ok || raw != "0" {
		t.Errorf("Raw(zeta) = %q, %v", raw, ok)
	}
	if got := m.Keys(); !reflect.DeepEqual(got, []string{"Zeta", "objectname", "other"}) {
		t.Errorf("Keys = %v", got)
	}
}

func TestMetaWritesExplicitZeros(t *testing.T) {
	src := "[U]{Zeta=0; objectname=; canhover=0; [damage]{}}"
	us := decodeMetaUnits(t, src)
	out, err := Marshal(us)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Zeta=0;", "objectname=;", "canhover=0;", "[damage]"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "missing") || strings.Contains(string(out), "alpha") {
		t.Errorf("absent keys must stay absent:\n%s", out)
	}
	if ok, msg := SemanticEqual([]byte(src), out); !ok {
		t.Errorf("strict comparison: %s\n%s", msg, out)
	}

	// Without a Meta the same struct drops them: the case Meta exists for.
	type plain struct {
		Name string `tdf:",name"`
		Zeta int    `tdf:"zeta,omitempty"`
	}
	var ps []plain
	_ = Unmarshal([]byte("[U]{Zeta=0;}"), &ps)
	out, _ = Marshal(ps)
	if ok, _ := SemanticEqual([]byte("[U]{Zeta=0;}"), out); ok {
		t.Error("SemanticEqual must tell an explicit zero from a missing key")
	}
}

func TestMetaKeepsSourceTextOrderAndSpelling(t *testing.T) {
	src := "[U]\n{\nZETA=7;\nMid=.60;\ncategory=ARM  KBOT;\ncanhover=3;\nother=x;\nAlpha=1.9;\n[DAMAGE]{zz=5; aa=05;}\n[child]{}\n}\n"
	us := decodeMetaUnits(t, src)
	u := us[0]
	if u.Zeta != 7 || u.Mid != 0.6 || u.Alpha != 1 || !u.Hover || len(u.Cats) != 2 || u.Damage["aa"] != 5 {
		t.Fatalf("decoded %+v", u)
	}
	out, err := Marshal(us)
	if err != nil {
		t.Fatal(err)
	}
	want := "[U]\n{\n\tZETA=7;\n\tMid=.60;\n\tcategory=ARM  KBOT;\n\tcanhover=3;\n\tother=x;\n\tAlpha=1.9;\n" +
		"\t[DAMAGE]\n\t{\n\t\tzz=5;\n\t\taa=05;\n\t}\n\t[child]\n\t{\n\t}\n}\n"
	if string(out) != want {
		t.Errorf("Marshal:\n%s\nwant:\n%s", out, want)
	}
	if ok, msg := SemanticEqual([]byte(src), out); !ok {
		t.Error(msg)
	}
	var viaDecoder []metaUnit
	if err := NewDecoder(strings.NewReader(src)).Decode(&viaDecoder); err != nil {
		t.Fatal(err)
	}
	var enc bytes.Buffer
	if err := NewEncoder(&enc).Encode(viaDecoder); err != nil || enc.String() != want {
		t.Errorf("Decoder/Encoder differ from Unmarshal/Marshal: %v\n%s", err, enc.String())
	}
}

func TestMetaChangedValuesUseNormalForm(t *testing.T) {
	us := decodeMetaUnits(t, "[U]{mid=.60; Alpha=1.9; canhover=3;}")
	us[0].Mid = 2.5
	us[0].Alpha = 1 // still what "1.9" reads as: text kept
	us[0].Hover = false
	us[0].Missing = 4 // new key: after the source's keys
	out, err := Marshal(us)
	if err != nil {
		t.Fatal(err)
	}
	want := "[U]\n{\n\tmid=2.5;\n\tAlpha=1.9;\n\tcanhover=0;\n\tmissing=4;\n}\n"
	if string(out) != want {
		t.Errorf("Marshal:\n%s\nwant:\n%s", out, want)
	}
}

func TestMetaMarkAndForget(t *testing.T) {
	us := decodeMetaUnits(t, "[U]{zeta=0;}")
	u := &us[0]
	copied := u.Meta
	u.Meta.Mark("Missing")
	u.Meta.Forget("zeta")
	if copied.Present("missing") || !copied.Present("zeta") {
		t.Error("changing one Meta must not change a copy")
	}
	out, _ := Marshal(us)
	if !strings.Contains(string(out), "Missing=0;") || strings.Contains(string(out), "zeta") {
		t.Errorf("Mark/Forget:\n%s", out)
	}
	var zero Meta
	zero.Mark("x")
	if !zero.Present("X") {
		t.Error("the zero Meta must be usable")
	}
}

func TestMetaInEmbeddedBaseAndPointer(t *testing.T) {
	type base struct {
		Name string `tdf:",name"`
		Meta *Meta  `tdf:",meta"`
	}
	type leaf struct {
		base
		Cost int `tdf:"cost,omitempty"`
	}
	var ls []leaf
	if err := Unmarshal([]byte("[L]{cost=0;}"), &ls); err != nil {
		t.Fatal(err)
	}
	if ls[0].Meta == nil || !ls[0].Meta.Present("cost") {
		t.Fatal("promoted *Meta not filled")
	}
	out, _ := Marshal(ls)
	if !strings.Contains(string(out), "cost=0;") {
		t.Errorf("Marshal:\n%s", out)
	}
}

func TestMetaDoesNotDuplicateTextIntoCatchAll(t *testing.T) {
	us := decodeMetaUnits(t, "[U]{mid=13O; alpha=2x;}")
	if len(us[0].Extra) != 0 {
		t.Errorf("with a Meta the text lives there, not in the catch-all: %v", us[0].Extra)
	}
	got := MisparsedKeys(us)
	if len(got) != 2 {
		t.Errorf("MisparsedKeys = %v", got)
	}
	out, _ := Marshal(us)
	if !strings.Contains(string(out), "mid=13O;") || !strings.Contains(string(out), "alpha=2x;") {
		t.Errorf("Marshal:\n%s", out)
	}
}

func TestMetaDuplicateSectionsKeepTheirPlace(t *testing.T) {
	type info struct {
		Side string `tdf:"side"`
	}
	type doc struct {
		Info info         `tdf:"UNITINFO"`
		Rest []anySection `tdf:",sections"`
		Meta Meta         `tdf:",meta"`
	}
	src := "[OTHER]{}\n[UNITINFO]{side=ARM;}\n[UNITINFO]{side=CORE;}\n"
	var d doc
	if err := Unmarshal([]byte(src), &d); err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	if ok, msg := SemanticEqual([]byte(src), out); !ok {
		t.Errorf("%s\n%s", msg, out)
	}
}
