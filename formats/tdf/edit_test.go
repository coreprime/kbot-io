package tdf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// bytesReadBack parses src, applies edit and checks that Bytes gives want and
// that the result loads as the edited document.
func bytesReadBack(t *testing.T, src string, edit func(*Document), want string) {
	t.Helper()
	doc, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	edit(doc)
	got, err := doc.Bytes()
	if err != nil {
		t.Fatalf("Bytes(%q): %v", src, err)
	}
	if string(got) != want {
		t.Errorf("Bytes(%q) = %q, want %q", src, got, want)
	}
	back, err := ParseString(string(got))
	if err != nil {
		t.Fatal(err)
	}
	if diff := diffSections(doc.root, back.root, ""); diff != "" {
		t.Errorf("Bytes(%q) = %q reads back with %s", src, got, diff)
	}
}

func TestDocumentBytesValueEndingInSlashBeforeComment(t *testing.T) {
	setX := func(v string) func(*Document) {
		return func(d *Document) { d.Section("A").Set("x", v) }
	}
	bytesReadBack(t, "[A]{x=5/*c*/;}", setX("a/"), "[A]{x=a/ /*c*/;}")
	bytesReadBack(t, "[A]{x=5//c\n;}", setX("a/"), "[A]{x=a/ //c\n;}")
	bytesReadBack(t, "[A]{x=5/*;", setX("a/"), "[A]{x=a/ /*;")
	// No comment after the value: nothing is added.
	bytesReadBack(t, "[A]{x=5 /*c*/;}", setX("a/"), "[A]{x=a/ /*c*/;}")
	bytesReadBack(t, "[A]{x=5;}", setX("a/"), "[A]{x=a/;}")
	// A value after a closed comment may start with '/' or '*'.
	bytesReadBack(t, "[A]{x=/*c*/5;}", setX("*a"), "[A]{x=/*c*/*a;}")
	bytesReadBack(t, "[A]{x=/*c*/5;}", setX("/a"), "[A]{x=/*c*//a;}")
}

func TestDocumentBytesAddsBeforeTrailingComment(t *testing.T) {
	bytesReadBack(t, "\n /*", func(d *Document) { d.AddSection("A") }, "\n \n[A]\n\t{\n\t}\n/*")
	bytesReadBack(t, "/*", func(d *Document) { d.Root().Set("x", "1") }, "x=1;\n/*")
	bytesReadBack(t, "/*A\n...a//c\n", func(d *Document) { d.Root().Set("x", "1") }, "x=1;\n/*A\n...a//c\n")
	// The comment's kept last byte reads as stray text; the addition goes
	// before the comment, not after that byte.
	bytesReadBack(t, "/*abc", func(d *Document) { d.Root().Set("x", "1") }, "x=1;\n/*abc")
	// A deleted statement ahead of the comment: add after it.
	bytesReadBack(t, "a=1; /*", func(d *Document) {
		d.Root().Delete("a")
		d.Root().Set("b", "2")
	}, " \nb=2;\n/*")
}

func TestDocumentBytesAddsBeforeTrailingStrayText(t *testing.T) {
	bytesReadBack(t, "foo", func(d *Document) { d.Root().Set("x", "1") }, "x=1;\nfoo")
	bytesReadBack(t, "// c\nfoo", func(d *Document) { d.AddSection("A") }, "// c\n[A]\n\t{\n\t}\nfoo")
	bytesReadBack(t, "a=1;\nfoo", func(d *Document) {
		d.Root().Delete("a")
		d.Root().Set("b", "2")
	}, "b=2;\nfoo")

	// With SkipStrayText the stray text before a stray '}' is dropped, but
	// text added after it would still join it.
	doc, err := ParseWith(strings.NewReader("foo }"), ParseOptions{SkipStrayText: true})
	if err != nil {
		t.Fatal(err)
	}
	doc.Root().Set("x", "1")
	got, err := doc.Bytes()
	if err != nil || string(got) != "x=1;\nfoo }" {
		t.Errorf("Bytes = %q, %v", got, err)
	}
}

func TestDocumentBytesClosesCommentBeforeKeptByte(t *testing.T) {
	// The ';' ending x is the text's last byte, kept from inside the
	// unterminated comment. Text added after it needs the comment closed.
	bytesReadBack(t, "x=1/*;", func(d *Document) { d.Root().Set("y", "2") }, "x=1/**/;\ny=2;")
	bytesReadBack(t, "[A]{x=1;/*}", func(d *Document) {
		d.Section("A").Set("y", "2")
		d.AddSection("B")
	}, "[A]{x=1;\n\ty=2;/**/}\n[B]\n\t{\n\t}")
	bytesReadBack(t, "[A]/*{", func(d *Document) { d.Section("A").Set("x", "1") }, "[A]/**/{\n\tx=1;")
	// An empty value inside the comment: the new value goes before it.
	bytesReadBack(t, "x=/*;", func(d *Document) { d.Root().Set("x", "a/") }, "x=a/ /*;")
	bytesReadBack(t, "x=/*;", func(d *Document) {
		d.Root().Set("x", "1")
		d.Root().Set("y", "2")
	}, "x=1/**/;\ny=2;")
	// A deleted statement takes its comment with it.
	bytesReadBack(t, "x=1/*;", func(d *Document) {
		d.Root().Delete("x")
		d.Root().Set("y", "2")
	}, "\ny=2;")
}

func TestDocumentBytesRefusesAdditionsItCannotPlace(t *testing.T) {
	for _, tc := range []struct {
		src  string
		edit func(*Document)
	}{
		// The text ends inside the value of x: y would join it.
		{"x=1", func(d *Document) { d.Root().Set("y", "2") }},
		// The text ends inside [A]: B would land in it.
		{"[A]{x=1;", func(d *Document) { d.AddSection("B") }},
		{"[A]{[B]{x=1;", func(d *Document) { d.Section("A").Set("y", "2") }},
	} {
		doc, err := ParseString(tc.src)
		if err != nil {
			t.Fatal(err)
		}
		tc.edit(doc)
		if got, err := doc.Bytes(); err == nil {
			t.Errorf("Bytes(%q) = %q, want an error", tc.src, got)
		}
	}
	// Inside the unterminated section the addition is fine.
	bytesReadBack(t, "[A]{x=1;", func(d *Document) { d.Section("A").Set("y", "2") }, "[A]{x=1;\n\ty=2;")
}

func TestDocumentBytesChecksItsResult(t *testing.T) {
	// splice alone would glue y onto the unterminated value; Bytes must not
	// return that text.
	doc, _ := ParseString("x=1")
	doc.Root().Set("y", "2")
	if err := doc.readsBack([]byte("x=1\ny=2;")); err == nil {
		t.Error("readsBack accepted text that reads back differently")
	}
	if err := doc.readsBack([]byte("x=1;y=2;")); err != nil {
		t.Errorf("readsBack: %v", err)
	}
}

// FuzzDocumentBytes edits parsed texts at random. Whatever the source, Bytes
// must either refuse or give text that reads back as the edited document;
// for a text the game accepts (a strict parse), placing the changes must
// succeed and read back without Bytes' own check.
func FuzzDocumentBytes(f *testing.F) {
	for _, s := range []string{
		"[A]{x=5/*c*/;}", "[A]{x=5//c\n;}", "\n /*", "/*", "/*A\n...a//c\n", "foo", "x=1/*;",
		"[A]{x=1;/*}", "[A]/*{", "a=1; [S]{b=2;} a=3;", "[A]\r\n{\r\n\tx=1;\r\n}\r\n",
		"[A]{x=1;}}rest", "x=1;\x00[B]{}", "[A]{[B]{x=1;}}", "// c\n[A]{}// d", "x=a/*b*/c;",
		"[A] x=1;", "x=1", "[A]{x=1;", "foo; x=1;", "\xef\xbb\xbfx=1;", "x=1;\n/*c*/", "=/*", "/*=", "x=/*;",
	} {
		f.Add(s, []byte{0, 0, 1, 2, 1, 3, 3, 0, 4})
	}
	values := []string{"1", "a/", "/", "*", "x*", "", "a b", "*/", "/x", "v\nw", "}", "{", "[", "="}
	keys := []string{"k", "/k", "*k", "k/", "K", "x", "a"}
	names := []string{"S", "/S", "*S", "A", ""}
	f.Fuzz(func(t *testing.T, src string, ops []byte) {
		if len(src) > 4096 || len(ops) > 60 {
			return
		}
		skip := len(ops) > 0 && ops[0]&0x80 != 0
		_, strictErr := ParseWith(strings.NewReader(src), ParseOptions{Strict: true, SkipStrayText: skip})
		doc, err := ParseWith(strings.NewReader(src), ParseOptions{SkipStrayText: skip})
		if err != nil {
			return
		}
		for i := 0; i+2 < len(ops); i += 3 {
			var secs []*Section
			var walk func(*Section)
			walk = func(s *Section) {
				secs = append(secs, s)
				for _, c := range s.Sections() {
					walk(c)
				}
			}
			walk(doc.root)
			s := secs[int(ops[i+1])%len(secs)]
			v := values[int(ops[i+2])%len(values)]
			switch ops[i] % 5 {
			case 0:
				if fs := s.Fields(); len(fs) > 0 {
					s.Set(fs[int(ops[i+2])%len(fs)].Key(), v)
				}
			case 1:
				if fs := s.Fields(); len(fs) > 0 {
					s.Delete(fs[int(ops[i+2])%len(fs)].Key())
				}
			case 2:
				s.Set(keys[int(ops[i+1])%len(keys)], v)
			case 3:
				s.AddSection(names[int(ops[i+2])%len(names)]).Set("n", "1")
			case 4:
				s.AddSection(fmt.Sprintf("N%d", i))
			}
		}
		readsBack := func(out []byte) {
			back, err := ParseWith(bytes.NewReader(out), ParseOptions{SkipStrayText: skip, MaxBytes: -1, MaxDepth: -1})
			if err != nil {
				t.Fatalf("edited %q = %q does not parse: %v", src, out, err)
			}
			if diff := diffSections(doc.root, back.root, ""); diff != "" {
				t.Fatalf("edited %q = %q reads back with %s", src, out, diff)
			}
		}
		if strictErr != nil {
			// A text the game refuses may have no place for an addition;
			// Bytes must then refuse rather than return the wrong text.
			if out, err := doc.Bytes(); err == nil {
				readsBack(out)
			}
			return
		}
		out, err := doc.splice()
		if err != nil {
			t.Fatalf("splice(%q) failed on a text the game accepts: %v", src, err)
		}
		readsBack(out)
		if _, err := doc.Bytes(); err != nil {
			t.Fatalf("Bytes(%q): %v", src, err)
		}
	})
}
