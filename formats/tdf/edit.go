package tdf

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Bytes returns the document as TDF text. For a document read by Parse it is
// the source text with only the changes made since then applied, so comments,
// spacing, line endings, key spelling, unchanged values and anything the game
// ignores (such as text after a stray '}') are kept byte for byte:
//
//   - a changed value replaces the text of the key's last assignment (the one
//     the game uses), leaving the rest of the statement alone; a space is
//     added after a new value ending in '/' when a comment follows it, so
//     the two do not merge into "//" or "/*";
//   - Delete removes every assignment of the key, and the line when nothing
//     else is on it;
//   - a new field or section is added after the last statement of its
//     section, indented as Write would, using the source's line endings. At
//     the top level of a text with no statement left, it goes before any
//     stray text or unterminated comment the text ends with, which would
//     otherwise swallow it.
//
// When the text ends inside an unterminated "/*", the game blanks everything
// after it except the text's last byte, so Bytes writes nothing there: a new
// value for an empty one inside the comment goes just before the comment, and
// when that last byte ends the statement (or opens the section) an addition
// must follow, Bytes first closes the comment with "*/" just before the byte,
// which then still ends the statement.
//
// Hashes the game takes over file or section text therefore change only when
// the data does. For a document built with NewDocument, Bytes gives the same
// text as Write.
//
// Like Write, Bytes fails on a key, value or section name that would not read
// back unchanged. It also reads its result back and fails, rather than return
// text that loads differently from the document, when the changes cannot be
// made in place: an addition to a section with no '{' in the source, or after
// a statement or section the source ends inside (a value with no ';' or a
// section with no '}' at the end of the text). Write can still write such a
// document.
func (d *Document) Bytes() ([]byte, error) {
	out, err := d.splice()
	if err != nil || d.src == nil {
		return out, err
	}
	if err := d.readsBack(out); err != nil {
		return nil, err
	}
	return out, nil
}

// splice applies the document's changes to its source text (or writes a new
// document), without reading the result back.
func (d *Document) splice() ([]byte, error) {
	if err := d.root.check(); err != nil {
		return nil, err
	}
	if d.src == nil {
		var b strings.Builder
		e := &errWriter{w: &b}
		d.root.writeItems(e, 0, "\n")
		return []byte(b.String()), e.err
	}
	c := &editCtx{src: d.src, nl: "\n", comment: d.comment, tail: d.tail}
	if bytes.Contains(d.src, []byte("\r\n")) {
		c.nl = "\r\n"
	}
	if err := d.root.collectEdits(c, 0); err != nil {
		return nil, err
	}
	return c.apply()
}

// readsBack reads out as Parse read the source and reports where it differs
// from the document.
func (d *Document) readsBack(out []byte) error {
	back, err := ParseWith(bytes.NewReader(out), ParseOptions{SkipStrayText: d.skip, MaxBytes: -1, MaxDepth: -1})
	if err != nil {
		return fmt.Errorf("tdf: cannot make these changes in place: the result does not read back: %w", err)
	}
	if diff := diffSections(d.root, back.root, ""); diff != "" {
		return fmt.Errorf("tdf: cannot make these changes in place: the result would read back with %s", diff)
	}
	return nil
}

// diffSections describes the first difference between the items of two
// sections, or returns "" when they hold the same fields and sections in the
// same order.
func diffSections(want, got *Section, path string) string {
	where := "the top level"
	if path != "" {
		where = path
	}
	for i := 0; i < len(want.items) && i < len(got.items); i++ {
		w, g := want.items[i], got.items[i]
		switch {
		case w.field != nil && g.field != nil:
			if w.field.key != g.field.key || w.field.value != g.field.value {
				return fmt.Sprintf("%s=%q in %s where %s=%q was set", g.field.key, g.field.value, where, w.field.key, w.field.value)
			}
		case w.section != nil && g.section != nil:
			if w.section.name != g.section.name {
				return fmt.Sprintf("section [%s] in %s where [%s] was set", g.section.name, where, w.section.name)
			}
			if diff := diffSections(w.section, g.section, path+"["+w.section.name+"]"); diff != "" {
				return diff
			}
		default:
			return fmt.Sprintf("a field and a section swapped at item %d of %s", i+1, where)
		}
	}
	if len(want.items) != len(got.items) {
		return fmt.Sprintf("%d items in %s where %d were set", len(got.items), where, len(want.items))
	}
	return ""
}

// editCtx gathers the edits Bytes makes to a source text.
type editCtx struct {
	src     []byte
	nl      string // the source's line ending
	comment int64  // offset of the unterminated "/*" the text ends in, or -1
	tail    int64  // start of stray top-level text after the last statement, or -1
	closed  bool   // "*/" has been added before the text's last byte
	edits   []textEdit
}

// textEdit replaces src[start:end] with text.
type textEdit struct {
	start, end int64
	text       string
}

// apply returns the source with the edits made.
func (c *editCtx) apply() ([]byte, error) {
	// By position; an insertion goes before a removal starting at the same
	// place.
	sort.SliceStable(c.edits, func(i, j int) bool {
		a, b := c.edits[i], c.edits[j]
		if a.start != b.start {
			return a.start < b.start
		}
		return a.start == a.end && b.start != b.end
	})
	var out bytes.Buffer
	var at int64
	for _, e := range c.edits {
		if e.start < at {
			return nil, fmt.Errorf("tdf: overlapping edits at offset %d", e.start)
		}
		out.Write(c.src[at:e.start])
		out.WriteString(e.text)
		at = e.end
	}
	out.Write(c.src[at:])
	return out.Bytes(), nil
}

// valueEdit returns the edit replacing the value text of a field's last
// assignment with its new value.
func (c *editCtx) valueEdit(f *Field) textEdit {
	v, text := f.src.value, f.value
	if st := f.src.stmts[len(f.src.stmts)-1]; c.comment >= 0 && st.start < c.comment && v.start > c.comment && v.start == v.end {
		// The old value is empty and the text ends inside an unterminated
		// comment opened after the '=': a value written where the old one
		// was would be blanked with the comment, so write it before the
		// comment.
		v = span{c.comment, c.comment}
	}
	if strings.HasSuffix(text, "/") && v.end < int64(len(c.src)) && (c.src[v.end] == '/' || c.src[v.end] == '*') {
		// The source has a comment right after the value; keep the new
		// value's '/' from joining its opening.
		text += " "
	}
	return textEdit{v.start, v.end, text}
}

// collectEdits gathers the edits for a section read by Parse whose fields
// sit at the given depth of indentation.
func (s *Section) collectEdits(c *editCtx, depth int) error {
	var added []item
	last := int64(-1) // end of the section's last remaining source statement
	cut := false      // the text ends inside that statement
	for _, it := range s.items {
		switch {
		case it.field != nil && it.field.src != nil:
			f := it.field
			if f.value != f.src.orig {
				c.edits = append(c.edits, c.valueEdit(f))
			}
			if end := f.src.stmts[len(f.src.stmts)-1].end; end > last {
				last, cut = end, f.src.cut
			}
		case it.section != nil && it.section.fromSrc:
			if err := it.section.collectEdits(c, depth+1); err != nil {
				return err
			}
			if it.end > last {
				last, cut = it.end, it.section.cut
			}
		default:
			added = append(added, it)
		}
	}
	removed := make([]textEdit, len(s.removed))
	for i, st := range s.removed {
		removed[i] = removal(c.src, st)
	}
	c.edits = append(c.edits, removed...)
	if len(added) == 0 {
		return nil
	}

	var at int64
	before := false // something the addition must stay ahead of follows at
	switch {
	case last >= 0:
		if cut {
			where := "the top level"
			if depth > 0 {
				where = "section [" + s.name + "]"
			}
			return fmt.Errorf("tdf: cannot add to %s in place: the source ends inside its last statement", where)
		}
		at = last
	case depth > 0:
		if s.open < 0 {
			return fmt.Errorf("tdf: cannot add to section [%s], which has no '{' in the source", s.name)
		}
		at = s.open
	default:
		// The top level has no statement left. Stray text or an
		// unterminated comment at its end would swallow text added after
		// it, so add before them, though not inside a deleted statement.
		at = s.stop
		if c.tail >= 0 {
			at = min(at, c.tail)
		}
		if c.comment >= 0 {
			at = min(at, c.comment)
		}
		for _, r := range removed {
			if r.start < at && at < r.end {
				at = r.end // the deletion takes the comment with it
			}
		}
		before = at < s.stop
	}
	if c.comment >= 0 && at > c.comment && (last >= 0 || depth > 0) {
		// The byte before at ends the statement (or opens the section) and
		// is the text's last byte, which the game keeps from inside an
		// unterminated comment. Text added after it would be blanked with
		// the comment and take that byte in, so close the comment first.
		if !c.closed {
			c.edits = append(c.edits, textEdit{at - 1, at - 1, "*/"})
			c.closed = true
		}
	}

	var b strings.Builder
	e := &errWriter{w: &b}
	(&Section{items: added}).writeItems(e, depth, c.nl)
	if e.err != nil {
		return e.err
	}
	text := strings.TrimSuffix(b.String(), c.nl)
	switch {
	case at == 0 || c.src[at-1] == '\n':
		text += c.nl // at the start of a line: end the added lines instead
	case before:
		text = c.nl + text + c.nl
	default:
		text = c.nl + text
	}
	c.edits = append(c.edits, textEdit{at, at, text})
	return nil
}

// removal returns the edit deleting a statement, widened to its whole line
// (line break included) when only spaces and tabs share the line with it.
func removal(src []byte, st span) textEdit {
	start, end := st.start, st.end
	ls := start
	for ls > 0 && (src[ls-1] == ' ' || src[ls-1] == '\t') {
		ls--
	}
	le := end
	for le < int64(len(src)) && (src[le] == ' ' || src[le] == '\t' || src[le] == '\r') {
		le++
	}
	if (ls == 0 || src[ls-1] == '\n') && (le == int64(len(src)) || src[le] == '\n') {
		start, end = ls, le
		if end < int64(len(src)) {
			end++
		}
	}
	return textEdit{start, end, ""}
}
