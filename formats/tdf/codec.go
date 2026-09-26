package tdf

import (
	"fmt"
	"strconv"
	"strings"
)

// tokenWriter is the subset of writer behaviour the TDF emitter needs. Both
// *strings.Builder (in-memory Marshal/Canonicalize) and *bufio.Writer (the
// streaming Encoder) satisfy it.
type tokenWriter interface {
	WriteString(string) (int, error)
	WriteByte(byte) error
}

// errWriter accumulates the first write error so the emitter can stay
// branch-free; callers check err once at the end.
type errWriter struct {
	w   tokenWriter
	err error
}

func (e *errWriter) str(s string) {
	if e.err == nil {
		_, e.err = e.w.WriteString(s)
	}
}

func (e *errWriter) ch(c byte) {
	if e.err == nil {
		e.err = e.w.WriteByte(c)
	}
}

// writeElems renders elements back to TDF text with tab indentation.
func writeElems(w tokenWriter, els []*element, depth int) error {
	e := &errWriter{w: w}
	for _, el := range els {
		e.writeElem(el, depth)
	}
	return e.err
}

func (e *errWriter) writeElem(el *element, depth int) {
	pad := strings.Repeat("\t", depth)
	if el.section {
		e.str(pad)
		e.ch('[')
		e.str(el.key)
		e.str("]\n")
		e.str(pad)
		e.str("{\n")
		for _, c := range el.children {
			e.writeElem(c, depth+1)
		}
		e.str(pad)
		e.str("}\n")
		return
	}
	e.str(pad)
	e.str(el.key)
	e.ch('=')
	e.str(el.value)
	e.str(";\n")
}

// Canonicalize parses TDF bytes and re-emits them with normalised whitespace
// and comments removed, keeping every section (in order, duplicates included)
// and every field value exactly as the game reads it. Text after a stray '}'
// outside any section is dropped, as the game ignores it. Running it twice is
// idempotent.
func Canonicalize(data []byte) ([]byte, error) {
	els, err := parseDocument(data)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	if err := writeElems(&b, els, 0); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// SemanticEqual reports whether two TDF documents are equivalent ignoring
// comments, whitespace, key/section-name case, field ordering, and numeric
// formatting (".6" == "0.60" == "0.6"). A field that is absent on one side is
// treated as equal to a zero-valued field on the other (a missing numeric or
// string key defaults to zero/empty in the engine), which lets `omitempty`
// struct fields round-trip semantically. When the documents differ, the second
// return value describes the first mismatch found.
func SemanticEqual(a, b []byte) (bool, string) {
	ea, err := parseDocument(a)
	if err != nil {
		return false, "parse a: " + err.Error()
	}
	eb, err := parseDocument(b)
	if err != nil {
		return false, "parse b: " + err.Error()
	}
	return elementsEqual(ea, eb, "")
}

func elementsEqual(a, b []*element, path string) (bool, string) {
	fieldsA, sectionsA := group(a)
	fieldsB, sectionsB := group(b)

	for k, va := range fieldsA {
		vb, ok := fieldsB[k]
		if !ok {
			if fieldListZeroish(va) {
				continue
			}
			return false, fmt.Sprintf("%s.%s present only in a (%v)", path, k, va)
		}
		if ok, msg := fieldListEqual(va, vb, path+"."+k); !ok {
			return false, msg
		}
	}
	for k, vb := range fieldsB {
		if _, ok := fieldsA[k]; ok {
			continue
		}
		if !fieldListZeroish(vb) {
			return false, fmt.Sprintf("%s.%s present only in b (%v)", path, k, vb)
		}
	}

	for k, sa := range sectionsA {
		sb := sectionsB[k]
		if ok, msg := sectionListEqual(sa, sb, path+"/"+k); !ok {
			return false, msg
		}
	}
	for k, sb := range sectionsB {
		if _, ok := sectionsA[k]; ok {
			continue
		}
		if ok, msg := sectionListEqual(nil, sb, path+"/"+k); !ok {
			return false, msg
		}
	}
	return true, ""
}

func group(els []*element) (fields map[string][]string, sections map[string][]*element) {
	fields = map[string][]string{}
	sections = map[string][]*element{}
	for _, el := range els {
		key := foldKey(el.key)
		if el.section {
			sections[key] = append(sections[key], el)
		} else {
			fields[key] = append(fields[key], el.value)
		}
	}
	return fields, sections
}

func fieldListEqual(a, b []string, path string) (bool, string) {
	// TDF field assignment is last-wins: when a key is repeated within a
	// section the engine keeps only the final value, so a typed struct that
	// collapses duplicates to one field is semantically equivalent to the
	// original. Compare the effective (last) value on each side.
	la, lb := a[len(a)-1], b[len(b)-1]
	if !valueEqual(la, lb) {
		return false, fmt.Sprintf("%s: %q vs %q", path, la, lb)
	}
	return true, ""
}

func sectionListEqual(a, b []*element, path string) (bool, string) {
	// A section that exists on only one side is acceptable when it carries no
	// meaningful (non-zero) data.
	if len(a) == 0 || len(b) == 0 {
		extra := a
		if len(a) == 0 {
			extra = b
		}
		for _, el := range extra {
			if !sectionZeroish(el) {
				return false, fmt.Sprintf("%s: section present on only one side with data", path)
			}
		}
		return true, ""
	}
	if len(a) != len(b) {
		return false, fmt.Sprintf("%s: %d vs %d sections", path, len(a), len(b))
	}
	for i := range a {
		if ok, msg := elementsEqual(a[i].children, b[i].children, path); !ok {
			return false, msg
		}
	}
	return true, ""
}

func valueEqual(a, b string) bool {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	// Runs of whitespace between tokens are insignificant in TDF values
	// (e.g. a space-delimited category list "WEAPON  NOTSUB"), so collapse
	// them before comparing.
	if strings.EqualFold(a, b) || strings.EqualFold(collapseSpaces(a), collapseSpaces(b)) {
		return true
	}
	fa, ea := strconv.ParseFloat(a, 64)
	fb, eb := strconv.ParseFloat(b, 64)
	return ea == nil && eb == nil && fa == fb
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func valueZeroish(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f == 0
}

func fieldListZeroish(vs []string) bool {
	for _, v := range vs {
		if !valueZeroish(v) {
			return false
		}
	}
	return true
}

func sectionZeroish(el *element) bool {
	for _, c := range el.children {
		if c.section {
			if !sectionZeroish(c) {
				return false
			}
		} else if !valueZeroish(c.value) {
			return false
		}
	}
	return true
}
