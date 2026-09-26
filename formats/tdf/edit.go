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
//     the game uses), leaving the rest of the statement alone;
//   - Delete removes every assignment of the key, and the line when nothing
//     else is on it;
//   - a new field or section is added after the last statement of its
//     section, indented as Write would, using the source's line endings.
//
// Hashes the game takes over file or section text therefore change only when
// the data does. For a document built with NewDocument, Bytes gives the same
// text as Write. Like Write, Bytes fails on text that would not read back
// unchanged.
func (d *Document) Bytes() ([]byte, error) {
	if err := d.root.check(); err != nil {
		return nil, err
	}
	if d.src == nil {
		var b strings.Builder
		e := &errWriter{w: &b}
		d.root.writeItems(e, 0, "\n")
		return []byte(b.String()), e.err
	}
	nl := "\n"
	if bytes.Contains(d.src, []byte("\r\n")) {
		nl = "\r\n"
	}
	var edits []textEdit
	if err := d.root.collectEdits(&edits, d.src, 0, nl); err != nil {
		return nil, err
	}
	// By position; an insertion goes before a removal starting at the same
	// place.
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].start == edits[i].end && edits[j].start != edits[j].end
	})
	var out bytes.Buffer
	var at int64
	for _, e := range edits {
		if e.start < at {
			return nil, fmt.Errorf("tdf: overlapping edits at offset %d", e.start)
		}
		out.Write(d.src[at:e.start])
		out.WriteString(e.text)
		at = e.end
	}
	out.Write(d.src[at:])
	return out.Bytes(), nil
}

// textEdit replaces src[start:end] with text.
type textEdit struct {
	start, end int64
	text       string
}

// collectEdits gathers the edits for a section read by Parse whose fields
// sit at the given depth of indentation.
func (s *Section) collectEdits(edits *[]textEdit, src []byte, depth int, nl string) error {
	var added []item
	last := int64(-1) // end of the section's last remaining source statement
	for _, it := range s.items {
		switch {
		case it.field != nil && it.field.src != nil:
			f := it.field
			if f.value != f.src.orig {
				*edits = append(*edits, textEdit{f.src.value.start, f.src.value.end, f.value})
			}
			for _, st := range f.src.stmts {
				last = max(last, st.end)
			}
		case it.section != nil && it.section.fromSrc:
			if err := it.section.collectEdits(edits, src, depth+1, nl); err != nil {
				return err
			}
			last = max(last, it.end)
		default:
			added = append(added, it)
		}
	}
	for _, st := range s.removed {
		*edits = append(*edits, removal(src, st))
	}
	if len(added) == 0 {
		return nil
	}

	at := last
	if at < 0 {
		at = s.open
		if depth == 0 {
			at = s.stop
		}
	}
	if at < 0 {
		return fmt.Errorf("tdf: cannot add to section [%s], which has no '{' in the source", s.name)
	}
	var b strings.Builder
	e := &errWriter{w: &b}
	(&Section{items: added}).writeItems(e, depth, nl)
	if e.err != nil {
		return e.err
	}
	text := strings.TrimSuffix(b.String(), nl)
	if at == 0 || src[at-1] == '\n' {
		text += nl // at the start of a line: end the added lines instead
	} else {
		text = nl + text
	}
	*edits = append(*edits, textEdit{at, at, text})
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
