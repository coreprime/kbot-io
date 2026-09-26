package tsf

import (
	"strings"
	"testing"
)

// untidyTSF exercises everything a hand-edited file may contain: space
// indentation, blank and comment lines inside sections, a comment spanning
// lines, trailing comments, mixed line endings, two top-level sections
// separated by a comment, and no final line terminator.
const untidyTSF = "/* header */\r\n" +
	"\r\n" +
	"[Anim]   // the animation\r\n" +
	"{\r\n" +
	"    Looping = 0;  /* never */\r\n" +
	"\r\n" +
	"    // first frame\n" +
	"    [Frame0]\r\n" +
	"    {\r\n" +
	"        Delay=3;\r\n" +
	"        /* a comment\r\n" +
	"           over two lines */\r\n" +
	"        [Layer0]\r\n" +
	"        {\r\n" +
	"            Filename = a.png\r\n" +
	"        }\r\n" +
	"    }\r\n" +
	"}\r\n" +
	"\r\n" +
	"// another\r\n" +
	"[Second]\r\n" +
	"{\r\n" +
	"}\r\n" +
	"trailing words"

func TestTSFRoundTripsUntidyText(t *testing.T) {
	doc, err := ParseTSF(untidyTSF)
	if err != nil {
		t.Fatalf("ParseTSF: %v", err)
	}
	if got := doc.String(); got != untidyTSF {
		t.Fatalf("round trip differs:\n got %q\nwant %q", got, untidyTSF)
	}
	if len(doc.Sections) != 2 || doc.Sections[1].Name != "Second" {
		t.Fatalf("sections = %d", len(doc.Sections))
	}
	anim := doc.Sections[0]
	if v, _ := anim.Get("Looping"); v != "0" {
		t.Errorf("Looping = %q", v)
	}
	frame := anim.Subsections()[0]
	if v, _ := frame.Get("Delay"); v != "3" {
		t.Errorf("Delay = %q", v)
	}
	if v, _ := frame.Subsections()[0].Get("Filename"); v != "a.png" {
		t.Errorf("Filename = %q", v)
	}
	trivia := 0
	for _, n := range anim.Body {
		if _, ok := n.(*Trivia); ok {
			trivia++
		}
	}
	if trivia != 2 {
		t.Errorf("animation body has %d trivia lines, want 2", trivia)
	}
	if doc.Trailing[0] != "trailing words" {
		t.Errorf("Trailing = %q", doc.Trailing)
	}
}

func TestTSFEditsRewriteOnlyChangedLines(t *testing.T) {
	doc, err := ParseTSF(untidyTSF)
	if err != nil {
		t.Fatal(err)
	}
	frame := doc.Sections[0].Subsections()[0]
	frame.Body[0].(*Assignment).Value = "7"
	frame.Body = append(frame.Body, &Assignment{Key: "Format", Value: "ARGB1555"})
	doc.Sections[1].Name = "Renamed"

	want := strings.NewReplacer(
		"        Delay=3;\r\n", "\t\tDelay = 7;\r\n",
		"        }\r\n    }\r\n", "        }\r\n\t\tFormat = ARGB1555;\r\n    }\r\n",
		"[Second]\r\n", "[Renamed]\r\n",
	).Replace(untidyTSF)
	if got := doc.String(); got != want {
		t.Fatalf("edited document:\n got %q\nwant %q", got, want)
	}
}

func TestTSFAssignmentValues(t *testing.T) {
	doc, err := ParseTSF("[A]\n{\n\tKey = one two ; // note\n\tBare = x\n}\n")
	if err != nil {
		t.Fatalf("ParseTSF: %v", err)
	}
	if v, _ := doc.Sections[0].Get("Key"); v != "one two" {
		t.Errorf("Key = %q", v)
	}
	if v, _ := doc.Sections[0].Get("Bare"); v != "x" {
		t.Errorf("Bare = %q", v)
	}
	if _, err := ParseTSF("[A]\n{\n\tKey = 1; Other = 2;\n}\n"); err == nil {
		t.Error("two statements on one line accepted")
	}
}
