package tdf

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestDocumentOneLineSections(t *testing.T) {
	doc, err := ParseString("[ARMLLT] {}\r\n[ARMVP] {}\r\n[ARMLAB] { x=1; }")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(doc.Sections()); n != 3 {
		t.Fatalf("got %d sections, want 3", n)
	}
	if got := doc.Section("armlab").Int("x"); got != 1 {
		t.Errorf("x = %d", got)
	}
}

func TestDocumentHeaderAndBraceOnOneLine(t *testing.T) {
	doc, err := ParseString("[UNITINFO] {\n\tUnitName=ARMCOM; Side=ARM;\n\tBuildCostMetal=2500;\n}")
	if err != nil {
		t.Fatal(err)
	}
	u := doc.Section("UNITINFO")
	if u == nil || u.String("UnitName") != "ARMCOM" || u.String("Side") != "ARM" || u.Int("BuildCostMetal") != 2500 {
		t.Fatalf("unit = %v", u)
	}
}

func TestDocumentStripsAllComments(t *testing.T) {
	doc, err := ParseString(`[WEAPON]
	{
	numlines=2;	// Radius of 1
	name=Immolator; //c
	}
/*
[VTOL_FOOMISSILE]
	{
	range=500;
	}
*/
`)
	if err != nil {
		t.Fatal(err)
	}
	w := doc.Section("WEAPON")
	if w.Int("numlines") != 2 || w.String("name") != "Immolator" {
		t.Errorf("numlines=%q name=%q", w.String("numlines"), w.String("name"))
	}
	if doc.Section("VTOL_FOOMISSILE") != nil || len(doc.Sections()) != 1 {
		t.Error("a commented-out section must not be read")
	}
}

func TestDocumentReadsLikeTheCodec(t *testing.T) {
	src := "[A]{x=1;\nflag=3;\nlist=a  b;\n[DAMAGE]{default=10;}\n}"
	doc, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		A struct {
			X      int            `tdf:"x"`
			Flag   bool           `tdf:"flag"`
			List   []string       `tdf:"list"`
			Damage map[string]int `tdf:"damage"`
		} `tdf:"A"`
	}
	if err := Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	if a.Int("x") != v.A.X || a.Bool("flag") != v.A.Flag || len(a.List("list")) != len(v.A.List) ||
		a.Section("DAMAGE").Int("default") != v.A.Damage["default"] {
		t.Errorf("document and codec disagree: %+v", v.A)
	}
}

func TestDocumentNumbersReadLikeTheGame(t *testing.T) {
	doc, err := ParseString("[A]{i=12abc; f=13O; d=1.5d2; big=4294967297; fx=0.15; b=2;}")
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	if a.Int("i") != 12 || a.Float("f") != 13 || a.Float("d") != 150 || a.Int("big") != 1 ||
		a.Fixed("fx") != 9830 || a.Bool("b") {
		t.Errorf("i=%d f=%v d=%v big=%d fx=%d b=%v", a.Int("i"), a.Float("f"), a.Float("d"),
			a.Int("big"), a.Fixed("fx"), a.Bool("b"))
	}
}

func TestDocumentFirstDuplicateSectionWins(t *testing.T) {
	doc, err := ParseString("[UNITINFO]{Side=ARM;}\n[unitinfo]{Side=CORE; Extra=1;}")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Section("UNITINFO").String("Side"); got != "ARM" {
		t.Errorf("Side = %q, want the first section's", got)
	}
	if n := len(doc.Sections()); n != 2 {
		t.Errorf("both sections should be kept, got %d", n)
	}
}

func TestDocumentLastAssignmentWins(t *testing.T) {
	doc, err := ParseString("[A]{Name=a; NAME=b; name=c;}")
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	fs := a.Fields()
	if a.String("Name") != "c" || len(fs) != 1 || fs[0].Key() != "Name" {
		t.Errorf("fields = %v", fs)
	}
}

func TestDocumentFoldsASCIIOnly(t *testing.T) {
	doc, err := ParseString("[CAF\xC9]{k\xE9=1;}\n[CAF\xC8]{v=2;}")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(doc.Sections()); n != 2 {
		t.Fatalf("sections = %d", n)
	}
	if doc.Section("caf\xC9").Int("K\xE9") != 1 || doc.Section("CAF\xC8").Int("v") != 2 {
		t.Error("ASCII letters must fold")
	}
	if doc.Section("CAF\xE9") != nil || doc.Section("caf\xC9").Has("K\xC9") {
		t.Error("bytes above 0x7F must not fold")
	}
}

func TestDocumentRootFields(t *testing.T) {
	doc, err := ParseString("SCHEMACOUNT=2;\n[A]{}\nafter=1;")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Root().Int("SCHEMACOUNT") != 2 || len(doc.Fields()) != 2 {
		t.Errorf("root fields = %v", doc.Fields())
	}
	out := doc.String()
	if !strings.HasPrefix(out, "SCHEMACOUNT=2;\n[A]\n") || !strings.HasSuffix(out, "after=1;\n") {
		t.Errorf("Write lost root fields or their order:\n%s", out)
	}
}

func TestDocumentWriteKeepsNestedSections(t *testing.T) {
	src := "[GADGET0]{[COMMON]{id=0; name=x;} active=1;}\n[GADGET1]{[COMMON]{id=1;}}"
	doc, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	want := "[GADGET0]\n\t{\n\t[COMMON]\n\t\t{\n\t\tid=0;\n\t\tname=x;\n\t\t}\n\tactive=1;\n\t}\n" +
		"[GADGET1]\n\t{\n\t[COMMON]\n\t\t{\n\t\tid=1;\n\t\t}\n\t}\n"
	if out != want {
		t.Errorf("Write:\n%s\nwant:\n%s", out, want)
	}
	if ok, msg := SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Error(msg)
	}
}

func TestDocumentWriteKeepsDuplicateSections(t *testing.T) {
	doc, err := ParseString("[A]{v=1;}\n[A]{v=2;}")
	if err != nil {
		t.Fatal(err)
	}
	out := doc.String()
	if !strings.Contains(out, "v=1;") || !strings.Contains(out, "v=2;") || strings.Count(out, "[A]") != 2 {
		t.Errorf("Write:\n%s", out)
	}
	added := doc.AddSection("B")
	added.SetInt("w", 3)
	if n := len(doc.Sections()); n != 3 || doc.Sections()[2] != added {
		t.Errorf("AddSection must show in Sections(): %d", n)
	}
	if doc.AddSection("a") != doc.Sections()[0] {
		t.Error("AddSection must return the first existing section")
	}
}

func TestDocumentParsesVeryLongLines(t *testing.T) {
	long := strings.Repeat("x", 100<<10)
	doc, err := ParseString("[A]{desc=" + long + ";}")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Section("A").String("desc"); got != long {
		t.Errorf("len = %d", len(got))
	}
}

func TestDocumentWriteRefusesUnrepresentableText(t *testing.T) {
	for _, set := range []func(s *Section){
		func(s *Section) { s.Set("desc", "See http://example.com") },
		func(s *Section) { s.Set("desc", "a;b") },
		func(s *Section) { s.Set("a=b", "1") },
		func(s *Section) { s.Set("k", " padded") },
		func(s *Section) { s.SetFloat("k", math.NaN()) },
	} {
		doc := NewDocument()
		set(doc.AddSection("A"))
		var b strings.Builder
		err := doc.Write(&b)
		if err == nil {
			t.Errorf("Write accepted %v", doc.Section("A").Fields()[0])
		}
		if b.Len() != 0 {
			t.Error("Write must not write anything when it fails")
		}
		if _, err := doc.Bytes(); err == nil {
			t.Error("Bytes accepted it too")
		}
	}
	doc := NewDocument()
	doc.AddSection("bad]name")
	if err := doc.Write(&strings.Builder{}); err == nil {
		t.Error("a ']' in a section name must be refused")
	}
	doc = NewDocument()
	doc.AddSection("A").SetFloat("inf", math.Inf(1))
	if got := doc.Section("A").Float("inf"); !math.IsInf(got, 1) {
		t.Errorf("+Inf reads back as %v", got)
	}
}

func TestDocumentDelete(t *testing.T) {
	doc, _ := ParseString("[A]{x=1; y=2;}")
	a := doc.Section("A")
	if !a.Delete("X") || a.Has("x") || a.Delete("x") {
		t.Error("Delete")
	}
	if fs := a.Fields(); len(fs) != 1 || fs[0].Key() != "y" {
		t.Errorf("fields = %v", fs)
	}
}

func TestDocumentBytesKeepsSourceText(t *testing.T) {
	src := "// header comment\r\n[UNITINFO]\r\n\t{\r\n\tUnitName=ARMCOM;  // the commander\r\n" +
		"\tBuildCostMetal = 2500 ;\r\n\tMaxDamage=3000;\r\n\t[SUB]{a=1;}\r\n\t}\r\n}trailing text the game ignores"
	doc, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	same, err := doc.Bytes()
	if err != nil || string(same) != src {
		t.Fatalf("unchanged document must give the source back: %v\n%q", err, same)
	}

	u := doc.Section("UNITINFO")
	u.SetInt("BuildCostMetal", 3000)  // changed: only the value text changes
	u.SetString("UnitName", "ARMCOM") // unchanged value: no edit
	u.Delete("MaxDamage")
	u.SetInt("Newkey", 5)
	u.Section("SUB").SetInt("b", 2)
	doc.AddSection("EXTRA").SetString("x", "y")
	got, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := "// header comment\r\n[UNITINFO]\r\n\t{\r\n\tUnitName=ARMCOM;  // the commander\r\n" +
		"\tBuildCostMetal = 3000 ;\r\n\t[SUB]{a=1;\r\n\t\tb=2;}\r\n\tNewkey=5;\r\n\t}\r\n" +
		"[EXTRA]\r\n\t{\r\n\tx=y;\r\n\t}\r\n}trailing text the game ignores"
	if string(got) != want {
		t.Errorf("Bytes:\n%q\nwant:\n%q", got, want)
	}
	back, err := ParseString(string(got))
	if err != nil {
		t.Fatal(err)
	}
	if back.Section("UNITINFO").Int("BuildCostMetal") != 3000 || back.Section("UNITINFO").Has("MaxDamage") ||
		back.Section("UNITINFO").Section("SUB").Int("b") != 2 || back.Section("EXTRA").String("x") != "y" {
		t.Errorf("edited text reads back wrong:\n%s", got)
	}
}

func TestDocumentBytesEditsTheLastAssignment(t *testing.T) {
	doc, _ := ParseString("[A]{x=1;\nx=2;\n}")
	doc.Section("A").SetInt("x", 9)
	got, _ := doc.Bytes()
	if string(got) != "[A]{x=1;\nx=9;\n}" {
		t.Errorf("Bytes = %q", got)
	}
	doc.Section("A").Delete("x")
	got, _ = doc.Bytes()
	if string(got) != "[A]{\n}" {
		t.Errorf("Delete removes every assignment: %q", got)
	}
}

func TestDocumentBytesIntoEmptySections(t *testing.T) {
	doc, _ := ParseString("[A]\n{\n}\n")
	doc.Section("A").SetInt("x", 1)
	got, _ := doc.Bytes()
	if string(got) != "[A]\n{\n\tx=1;\n}\n" {
		t.Errorf("Bytes = %q", got)
	}

	doc, _ = ParseString("[A]{x=1;}")
	doc.Section("A").Delete("x")
	doc.Section("A").SetInt("y", 2)
	got, _ = doc.Bytes()
	if string(got) != "[A]{\n\ty=2;}" {
		t.Errorf("Bytes = %q", got)
	}

	doc, _ = ParseString("// only a comment\n")
	doc.AddSection("A").SetInt("x", 1)
	got, _ = doc.Bytes()
	if string(got) != "// only a comment\n[A]\n\t{\n\tx=1;\n\t}\n" {
		t.Errorf("Bytes = %q", got)
	}

	doc, _ = ParseString("[A] x=1;")
	doc.Section("A").SetInt("y", 1)
	if _, err := doc.Bytes(); err == nil {
		t.Error("adding to a section with no braces should fail")
	}

	fresh := NewDocument()
	fresh.AddSection("A").SetInt("x", 1)
	got, _ = fresh.Bytes()
	if string(got) != fresh.String() {
		t.Errorf("a new document's Bytes should match Write: %q", got)
	}
}

func TestDocumentDiagnostics(t *testing.T) {
	var seen []Diagnostic
	doc, err := ParseWith(strings.NewReader("[A]{x=1\n}"), ParseOptions{OnDiagnostic: func(d Diagnostic) { seen = append(seen, d) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Diagnostics()) == 0 || len(seen) != len(doc.Diagnostics()) {
		t.Errorf("Diagnostics = %v, callback saw %v", doc.Diagnostics(), seen)
	}
	_, err = ParseWith(strings.NewReader("[A]{x=1\n}"), ParseOptions{Strict: true})
	var se *SyntaxError
	if !errors.As(err, &se) || se.Kind != DiagMissingSemicolon {
		t.Errorf("strict: %v", err)
	}
}
