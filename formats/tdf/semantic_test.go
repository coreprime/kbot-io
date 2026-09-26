package tdf

import "testing"

func TestSemanticEqualFollowsTheGame(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
		why  string
	}{
		{"[X]{a=0;}", "[X]{}", false, "an explicit zero is not a missing key"},
		{"[X]{s=;}", "[X]{}", false, "an explicit empty value is not a missing key"},
		{"[X]{}", "", false, "an empty section is not a missing one"},
		{"[X]{a=.6;}", "[X]{A=0.60;}", true, "numerals that read alike"},
		{"[X]{a=1;}", "[X]{a=1.0;}", true, "numerals that read alike"},
		{"[X]{a=1e3;}", "[X]{a=1000;}", false, "Atol reads 1 and 1000"},
		{"[X]{a=13O;}", "[X]{a=13;}", false, "not a plain numeral"},
		{"[X]{n=Big;}", "[X]{n=BIG;}", false, "value case matters"},
		{"[X]{c=A  B;}", "[X]{c=A B;}", false, "value spacing matters"},
		{"[x]{a=1;}", "[X]{A=1;}", true, "key and name case do not"},
		{"[X]{a=1; a=2;}", "[X]{a=2;}", true, "the last assignment wins"},
		{"[X]{a=1; A=2;}", "[X]{A=1; a=2;}", true, "whatever its case"},
		{"[X]{a=1; A=2;}", "[X]{A=2; a=1;}", false, "reordering case variants changes the winner"},
		{"[A]{}[B]{}", "[B]{}[A]{}", false, "section order matters"},
		{"[A]{v=1;}[A]{v=2;}", "[A]{v=1;}", false, "every duplicate section counts"},
		{"[A]{v=1;}[A]{v=2;}", "[A]{v=2;}[A]{v=1;}", false, "and so does their order"},
		{"[A]{x=1;}}[B]{}", "[A]{x=1;}", true, "text after a stray '}' is ignored"},
		{"[A]{x=1; // c\n}", "/* c */[A]\n{\n\tx = 1 ;\n}", true, "comments and layout"},
	} {
		if got, msg := SemanticEqual([]byte(c.a), []byte(c.b)); got != c.want {
			t.Errorf("%s: SemanticEqual(%q, %q) = %v (%s)", c.why, c.a, c.b, got, msg)
		}
	}
}

func TestSemanticEqualWithOptions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		opts CompareOptions
	}{
		{"[X]{a=0; s=;}", "[X]{}", CompareOptions{AbsentIsZero: true}},
		{"[X]{}[Y]{z=0;}", "[X]{}", CompareOptions{AbsentIsZero: true}},
		{"[X]{n=Big;}", "[X]{n=BIG;}", CompareOptions{FoldValueCase: true}},
		{"[X]{c=A  B;}", "[X]{c=A B;}", CompareOptions{CollapseSpace: true}},
		{"[A]{}[B]{}", "[B]{}[A]{}", CompareOptions{IgnoreSectionOrder: true}},
	} {
		if got, msg := SemanticEqualWith([]byte(c.a), []byte(c.b), c.opts); !got {
			t.Errorf("%+v: %q vs %q: %s", c.opts, c.a, c.b, msg)
		}
		if got, _ := SemanticEqual([]byte(c.a), []byte(c.b)); got {
			t.Errorf("%q vs %q should differ without options", c.a, c.b)
		}
	}
	if got, _ := SemanticEqualWith([]byte("[X]{a=1;}"), []byte("[X]{}"), CompareOptions{AbsentIsZero: true}); got {
		t.Error("AbsentIsZero must still see a non-zero value")
	}
}
