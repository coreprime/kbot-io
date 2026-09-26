package tdf

import (
	"errors"
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
