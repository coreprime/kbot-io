package tdf

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// blankText runs the comment blanker over s and returns what the tokenizer
// sees.
func blankText(s string) string {
	b := &blanker{r: bufio.NewReader(strings.NewReader(s))}
	var out []byte
	for {
		c, ok := b.next()
		if !ok {
			return string(out)
		}
		out = append(out, c)
	}
}

// diagKinds lists the kinds Diagnose reports for s.
func diagKinds(s string) []DiagKind {
	var out []DiagKind
	for _, d := range Diagnose([]byte(s)) {
		out = append(out, d.Kind)
	}
	return out
}

func hasKind(ks []DiagKind, k DiagKind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

// strictKind returns the kind of the *SyntaxError strict reading gives, or 0.
func strictKind(t *testing.T, s string) DiagKind {
	t.Helper()
	var v []struct {
		Name string `tdf:",name"`
	}
	err := UnmarshalWith([]byte(s), &v, ParseOptions{Strict: true})
	if err == nil {
		return 0
	}
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("strict %q: error %v is not a *SyntaxError", s, err)
	}
	return se.Kind
}

func TestCommentsAreBlankedByteForByte(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a/*b*/c/*/d*/e//f\ng/* hij", "a     c      e   \ng     j"},
		{"x//y\r\nz", "x    \nz"}, // the CR before the line break is comment too
		{"a/**/b", "a    b"},
		{"a/*/", "a  /"},       // "/*/" does not close; the last byte survives
		{"a/*", "a  "},         // nothing after "/*" survives
		{"a/* x *", "a     *"}, // the final byte is kept even when it is '*'
		{"a/*x*//y", "a     /y"},
		{"a/b*c", "a/b*c"},
		{"x/", "x/"},
	} {
		if got := blankText(c.in); got != c.want {
			t.Errorf("blank(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCommentInsideValueKeepsItsWidth(t *testing.T) {
	doc, err := ParseString("[A]{Name=Big/* x */Bertha; n=2;\t// Radius of 1\n}")
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	if got := a.String("Name"); got != "Big       Bertha" {
		t.Errorf("Name = %q, want the comment's 7 bytes as spaces", got)
	}
	if got := a.Int("n"); got != 2 {
		t.Errorf("n = %d, want 2 (trailing comment stripped)", got)
	}
}

func TestLineCommentInsideValueSwallowsTheNextKey(t *testing.T) {
	doc, err := ParseString("[A]{url=http://x;\nb=2;}")
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	url := a.String("url")
	if !strings.HasPrefix(url, "http:") || !strings.Contains(url, "b=2") {
		t.Errorf("url = %q, want it to run to the ';' after b=2", url)
	}
	if a.Has("b") {
		t.Error("b should be swallowed by url")
	}
}

func TestUnterminatedCommentKeepsTheLastByte(t *testing.T) {
	src := "[A]{x=1;}/* note"
	if k := strictKind(t, src); k != DiagMissingEquals {
		t.Errorf("strict: got %v, want the surviving 'e' to be text with no '='", k)
	}
	ks := diagKinds(src)
	if !hasKind(ks, DiagUnterminatedComment) || !hasKind(ks, DiagMissingEquals) {
		t.Errorf("diagnostics = %v", ks)
	}
	var v struct {
		A struct {
			X int `tdf:"x"`
		} `tdf:"A"`
	}
	if err := Unmarshal([]byte(src), &v); err != nil || v.A.X != 1 {
		t.Errorf("lenient: %v, x=%d", err, v.A.X)
	}
}

func TestValueRunsToTheNextSemicolon(t *testing.T) {
	doc, err := ParseString("[A]{a=1\nb=2;c=3;}")
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	if got := a.String("a"); got != "1\nb=2" {
		t.Errorf("a = %q", got)
	}
	if a.Has("b") || a.String("c") != "3" {
		t.Errorf("b present=%v c=%q", a.Has("b"), a.String("c"))
	}
	if ds := doc.Diagnostics(); len(ds) != 1 || ds[0].Kind != DiagValueLineBreak {
		t.Errorf("diagnostics = %v", doc.Diagnostics())
	}
}

func TestValueRunsPastABrace(t *testing.T) {
	// A missing ';' before '}' runs the value on to the next ';', swallowing
	// the section end and the next header; the game never sees section B.
	doc, err := ParseString("[A]{x=1}[B]{y=2;}")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Section("A").String("x"); got != "1}[B]{y=2" {
		t.Errorf("x = %q", got)
	}
	if doc.Section("B") != nil {
		t.Error("section B should not exist")
	}
	if ks := diagKinds("[A]{x=1}[B]{y=2;}"); !hasKind(ks, DiagValueBrace) {
		t.Errorf("diagnostics = %v", ks)
	}
}

func TestStrayRootBraceEndsTheText(t *testing.T) {
	src := "[A]{v=1;}\n}\n[B]{v=2;}"
	doc, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Section("A") == nil || doc.Section("B") != nil {
		t.Errorf("sections = %d, want only A", len(doc.Sections()))
	}
	if ks := diagKinds(src); !hasKind(ks, DiagStrayBrace) {
		t.Errorf("diagnostics = %v", ks)
	}
	if k := strictKind(t, src); k != 0 {
		t.Errorf("strict: %v, want accepted (the game accepts it)", k)
	}
	canon, err := Canonicalize([]byte(src))
	if err != nil || strings.Contains(string(canon), "[B]") {
		t.Errorf("Canonicalize kept ignored text: %v\n%s", err, canon)
	}
}

func TestStrayTextGluesOntoTheNextKey(t *testing.T) {
	src := "[A]{x=1;;y=2;}"
	doc, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	if a.Has("y") || a.String(";y") != "2" {
		t.Errorf("fields = %v", a.Fields())
	}
	if ks := diagKinds(src); !hasKind(ks, DiagSuspiciousKey) {
		t.Errorf("diagnostics = %v", ks)
	}

	skip, err := ParseWith(strings.NewReader(src), ParseOptions{SkipStrayText: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := skip.Section("A").String("y"); got != "2" {
		t.Errorf("SkipStrayText: y = %q", got)
	}
	skip2, _ := ParseWith(strings.NewReader("[A]{ junk }[B]{ {x=1; }"), ParseOptions{SkipStrayText: true})
	if skip2.Section("A") == nil || skip2.Section("B").String("x") != "1" {
		t.Errorf("SkipStrayText: sections %v", skip2.Sections())
	}
}

func TestOnlyFourSeparators(t *testing.T) {
	doc, err := ParseString("[A]{\fx=1;\vy =2;}")
	if err != nil {
		t.Fatal(err)
	}
	a := doc.Section("A")
	if a.Has("x") || a.String("\fx") != "1" || a.String("\vy") != "2" {
		t.Errorf("form feed and vertical tab must stay part of the key: %v", a.Fields())
	}
}

func TestLeadingBOMIsText(t *testing.T) {
	src := "\xEF\xBB\xBF[UNITINFO]{x=1;}"
	doc, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Section("UNITINFO") != nil {
		t.Error("the byte order mark should swallow the header into a key")
	}
	if ks := diagKinds(src); !hasKind(ks, DiagLeadingBOM) {
		t.Errorf("diagnostics = %v", ks)
	}
}

func TestNULEndsTheText(t *testing.T) {
	doc, err := ParseString("[A]{x=1;}\x00[B]{y=2;}")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Section("A") == nil || doc.Section("B") != nil {
		t.Errorf("want only A, got %d sections", len(doc.Sections()))
	}
	if ks := diagKinds("[A]{x=1;}\x00[B]{y=2;}"); !hasKind(ks, DiagEmbeddedNUL) {
		t.Errorf("diagnostics = %v", ks)
	}
	if k := strictKind(t, "[A]{x=1;\x00y=2;}"); k != DiagUnexpectedEnd {
		t.Errorf("NUL inside a section, strict: %v", k)
	}
}

func TestEmptyInput(t *testing.T) {
	if ks := diagKinds(""); len(ks) != 1 || ks[0] != DiagEmptyInput {
		t.Errorf("diagnostics = %v", ks)
	}
	if k := strictKind(t, ""); k != DiagEmptyInput {
		t.Errorf("strict: %v", k)
	}
	if ks := diagKinds("  \n"); len(ks) != 0 {
		t.Errorf("whitespace-only input is not empty: %v", ks)
	}
}

func TestStrictModeRejectsWhatTheGameRejects(t *testing.T) {
	for _, c := range []struct {
		src  string
		want DiagKind
		off  int64
	}{
		{"[A]{x}", DiagMissingEquals, 4},
		{"[A]{x=1}", DiagMissingSemicolon, 6},
		{"[A{x=1;}", DiagMissingBracket, 0},
		{"[A] x=1;", DiagMissingBrace, 4},
		{"[A]{x=1;", DiagUnexpectedEnd, 8},
		{"[A]{[B]{}", DiagUnexpectedEnd, 9},
	} {
		var v []struct{}
		err := UnmarshalWith([]byte(c.src), &v, ParseOptions{Strict: true})
		var se *SyntaxError
		if !errors.As(err, &se) || se.Kind != c.want || se.Offset != c.off {
			t.Errorf("%q: err = %v, want %v at %d", c.src, err, c.want, c.off)
		}
	}
	for _, src := range []string{"[A]{x=1;}", "[A] {}\r\n[B] { x=1; }", "key=value;", "[]{x=1;}", "[A]{}}junk"} {
		if k := strictKind(t, src); k != 0 {
			t.Errorf("%q: strict rejected with %v", src, k)
		}
	}
}

func TestLenientModeRepairsAndReports(t *testing.T) {
	for _, c := range []struct {
		src  string
		want DiagKind
	}{
		{"[A]{x}", DiagMissingEquals},
		{"[A]{x=1}", DiagMissingSemicolon},
		{"[A] x=1;", DiagMissingBrace},
		{"[A]{x=1;", DiagUnexpectedEnd},
	} {
		doc, err := ParseString(c.src)
		if err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if doc.Section("A") == nil {
			t.Errorf("%q: section A missing", c.src)
		}
		if !hasKind(diagKinds(c.src), c.want) {
			t.Errorf("%q: diagnostics %v, want %v", c.src, diagKinds(c.src), c.want)
		}
	}
	// A missing ']' cannot be repaired.
	if _, err := ParseString("[A{x=1;}"); err == nil {
		t.Error("missing ']' should fail")
	}
	// The header-without-brace repair keeps the following field.
	doc, _ := ParseString("[A] x=1;")
	if doc.Root().String("x") != "1" {
		t.Errorf("x = %q", doc.Root().String("x"))
	}
}

func TestDiagnosticOffsetsAndSections(t *testing.T) {
	src := "[A]\n{\n\t[B]\n\t{\n\tv=1\n\t}\n}\n"
	ds := Diagnose([]byte(src))
	var found bool
	for _, d := range ds {
		if d.Kind == DiagValueLineBreak {
			found = true
			if d.Section != "A/B" || d.Key != "v" || d.Offset != int64(strings.Index(src, "1")) {
				t.Errorf("diagnostic = %+v", d)
			}
			if !strings.Contains(d.String(), "[A/B]") {
				t.Errorf("String() = %q", d.String())
			}
		}
	}
	if !found {
		t.Errorf("no line-break diagnostic in %v", ds)
	}
}

func TestDuplicateDiagnostics(t *testing.T) {
	ks := diagKinds("[A]{x=1; X=2; [S]{} [s]{}} [a]{}")
	n := 0
	for _, k := range ks {
		if k == DiagDuplicateSection {
			n++
		}
	}
	if !hasKind(ks, DiagDuplicateKey) || n != 2 {
		t.Errorf("diagnostics = %v", ks)
	}
}

func TestDepthLimit(t *testing.T) {
	deep := strings.Repeat("[x]{", 80)
	var v []struct{}
	err := Unmarshal([]byte(deep), &v)
	var se *SyntaxError
	if !errors.As(err, &se) || se.Kind != DiagTooDeep {
		t.Fatalf("80 levels: %v", err)
	}
	if err := UnmarshalWith([]byte(deep), &v, ParseOptions{MaxDepth: -1}); err != nil {
		t.Errorf("no limit: %v", err)
	}
	if err := UnmarshalWith([]byte("[a]{[b]{[c]{}}}"), &v, ParseOptions{MaxDepth: 2}); err == nil {
		t.Error("MaxDepth 2 should refuse 3 levels")
	}
	// Hostile nesting fails cleanly instead of exhausting the stack, in the
	// streaming decoder too.
	huge := strings.Repeat("[x]{", 200000)
	if err := NewDecoder(strings.NewReader(huge)).Decode(&v); err == nil {
		t.Error("200000 levels should fail")
	}
}

func TestSizeLimit(t *testing.T) {
	src := []byte("[A]{x=1;}")
	var v []struct{}
	err := UnmarshalWith(src, &v, ParseOptions{MaxBytes: int64(len(src) - 1)})
	var se *SyntaxError
	if !errors.As(err, &se) || se.Kind != DiagTooLarge {
		t.Errorf("over the limit: %v", err)
	}
	if err := UnmarshalWith(src, &v, ParseOptions{MaxBytes: int64(len(src))}); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	if _, err := ParseWith(bytes.NewReader(src), ParseOptions{MaxBytes: 4}); err == nil {
		t.Error("ParseWith should apply the limit")
	}
}

func TestTrimsSeparatorsOnly(t *testing.T) {
	doc, err := ParseString("[ A ]{ \t key \r\n = \n value \t ; }")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Section("A").String("key"); got != "value" {
		t.Errorf("value = %q", got)
	}
	doc, _ = ParseString("[A]{k= v ;}")
	if got := doc.Section("A").String("k"); got != " v " {
		t.Errorf("non-ASCII space must not be trimmed: %q", got)
	}
}

func TestOnDiagnosticCallback(t *testing.T) {
	var got []Diagnostic
	var v []struct{}
	if err := UnmarshalWith([]byte("[A]{x=1;}}"), &v, ParseOptions{OnDiagnostic: func(d Diagnostic) { got = append(got, d) }}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != DiagStrayBrace || got[0].Offset != 9 {
		t.Errorf("diagnostics = %v", got)
	}
}

func TestDecoderReadsLikeUnmarshal(t *testing.T) {
	for _, src := range []string{
		"[A]{a=1\nb=2;}",
		"[A]{v=1;}\n}\n[B]{v=2;}",
		"[A]{x=1;;y=2;}",
		"[A]{x=1;}\x00[B]{y=2;}",
		"[A]{Name=Big/* x */Bertha;}/* tail",
	} {
		type sec struct {
			Name  string            `tdf:",name"`
			Extra map[string]string `tdf:",remaining"`
		}
		var a, b []sec
		errA := Unmarshal([]byte(src), &a)
		errB := NewDecoder(&oneByteReader{data: []byte(src)}).Decode(&b)
		if (errA == nil) != (errB == nil) || len(a) != len(b) {
			t.Errorf("%q: Unmarshal %v %v, Decode %v %v", src, errA, a, errB, b)
			continue
		}
		for i := range a {
			if a[i].Name != b[i].Name || len(a[i].Extra) != len(b[i].Extra) {
				t.Errorf("%q: %v vs %v", src, a[i], b[i])
			}
			for k, v := range a[i].Extra {
				if b[i].Extra[k] != v {
					t.Errorf("%q: %s = %q vs %q", src, k, v, b[i].Extra[k])
				}
			}
		}
	}
}
