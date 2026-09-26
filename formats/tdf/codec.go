package tdf

import (
	"fmt"
	"sort"
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

// writeElems renders elements back to TDF text with tab indentation. It
// refuses (before writing it) any key, value or section name that would not
// read back unchanged.
func writeElems(w tokenWriter, els []*element, depth int) error {
	if err := checkElems(els); err != nil {
		return err
	}
	e := &errWriter{w: w}
	for _, el := range els {
		e.writeElem(el, depth)
	}
	return e.err
}

func checkElems(els []*element) error {
	for _, el := range els {
		if el.section {
			if err := CheckName(el.key); err != nil {
				return err
			}
			if err := checkElems(el.children); err != nil {
				return err
			}
			continue
		}
		if err := CheckKey(el.key); err != nil {
			return err
		}
		if err := CheckValue(el.value); err != nil {
			return fmt.Errorf("%w (key %q)", err, el.key)
		}
	}
	return nil
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

// WriteError reports text that cannot be written as TDF because the game
// would read something else back.
type WriteError struct {
	What   string // "key", "value" or "section name"
	Text   string
	Reason string
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("tdf: %s %q cannot be written: %s", e.What, e.Text, e.Reason)
}

// checkText applies the rules every written token shares. The grammar has no
// escaping: a NUL ends the text, "//" and "/*" start comments, and the game
// trims spaces, tabs, CRs and LFs from both ends of every token.
func checkText(what, s string) error {
	switch {
	case strings.IndexByte(s, 0) >= 0:
		return &WriteError{what, s, "contains a NUL byte, which ends the text"}
	case strings.Contains(s, "//") || strings.Contains(s, "/*"):
		return &WriteError{what, s, "contains a comment start (// or /*)"}
	case s != trimSeparators(s):
		return &WriteError{what, s, "starts or ends with a space, tab or line break, which is trimmed"}
	}
	return nil
}

// CheckKey reports whether key can be written as a field key and read back
// unchanged: it must not contain '=' (which ends a key), start with '[' or '}'
// (which start a section or end one), contain a NUL, "//" or "/*", or start or
// end with a separator.
func CheckKey(key string) error {
	if err := checkText("key", key); err != nil {
		return err
	}
	if strings.IndexByte(key, '=') >= 0 {
		return &WriteError{"key", key, "contains '=', which ends a key"}
	}
	if key != "" && (key[0] == '[' || key[0] == '}') {
		return &WriteError{"key", key, "starts with '[' or '}', which reads as a section boundary"}
	}
	return nil
}

// CheckValue reports whether value can be written as a field value and read
// back unchanged: it must not contain ';' (which ends a value), a NUL, "//" or
// "/*", or start or end with a separator. Braces, brackets, '=' and line
// breaks are allowed: a value runs to the next ';' whatever it contains.
func CheckValue(value string) error {
	if err := checkText("value", value); err != nil {
		return err
	}
	if strings.IndexByte(value, ';') >= 0 {
		return &WriteError{"value", value, "contains ';', which ends a value"}
	}
	return nil
}

// CheckName reports whether name can be written as a section name and read
// back unchanged: it must not contain ']' (which ends a name), a NUL, "//" or
// "/*", or start or end with a separator.
func CheckName(name string) error {
	if err := checkText("section name", name); err != nil {
		return err
	}
	if strings.IndexByte(name, ']') >= 0 {
		return &WriteError{"section name", name, "contains ']', which ends a section name"}
	}
	return nil
}

// Canonicalize parses TDF bytes and re-emits them with normalised whitespace
// and comments removed, keeping every section (in order, duplicates included)
// and every field value exactly as the game reads it. Text after a stray '}'
// outside any section is dropped, as the game ignores it. Running it twice is
// idempotent. The output is not byte-identical to the input, so hashes the game
// takes over file or section bytes change (see the package documentation).
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

// CompareOptions loosens SemanticEqualWith. The zero value compares the way the
// game loads the two texts.
type CompareOptions struct {
	// AbsentIsZero treats a key missing on one side as equal to an explicit
	// zero or empty value on the other, and a section missing on one side as
	// equal to one holding only such values. The game applies its own
	// default to a missing key, which is often not zero, so this hides real
	// differences; it suits comparisons against writers that drop zero
	// values. It implies IgnoreSectionOrder.
	AbsentIsZero bool
	// IgnoreSectionOrder matches sections by name rather than by position.
	// Readers that walk sections by index see a different order.
	IgnoreSectionOrder bool
	// FoldValueCase compares values ignoring ASCII case.
	FoldValueCase bool
	// CollapseSpace compares values with each run of separators reduced to
	// one space.
	CollapseSpace bool
}

// SemanticEqual reports whether two TDF texts load the same data in the game.
// It ignores comments, formatting, the order of fields within a section, the
// ASCII case of keys and section names, earlier assignments of a key that is
// assigned again (the last one wins), and text after a stray '}' outside any
// section. It does not ignore a key present on one side only, even when its
// value is zero or empty, since the game applies a default to a missing key; the
// order or number of sections; or the case or spacing of values. Two values
// also match when both are plain numerals that read as the same number with
// both Atol and Atof (".6" and "0.60", "1" and "1.0"). When the texts differ,
// the second result describes the first difference found.
func SemanticEqual(a, b []byte) (bool, string) {
	return SemanticEqualWith(a, b, CompareOptions{})
}

// SemanticEqualWith is SemanticEqual with some differences ignored.
func SemanticEqualWith(a, b []byte, opts CompareOptions) (bool, string) {
	ea, err := parseDocument(a)
	if err != nil {
		return false, "parse a: " + err.Error()
	}
	eb, err := parseDocument(b)
	if err != nil {
		return false, "parse b: " + err.Error()
	}
	if opts.AbsentIsZero {
		opts.IgnoreSectionOrder = true
	}
	c := comparer{opts}
	return c.elements(ea, eb, "")
}

type comparer struct{ opts CompareOptions }

func (c comparer) elements(a, b []*element, path string) (bool, string) {
	fieldsA, sectionsA := group(a)
	fieldsB, sectionsB := group(b)

	for _, k := range sortedKeys(fieldsA) {
		va := fieldsA[k]
		vb, ok := fieldsB[k]
		if !ok {
			if c.opts.AbsentIsZero && valueZeroish(va) {
				continue
			}
			return false, fmt.Sprintf("%s.%s present only in a (%q)", path, k, va)
		}
		if !c.valueEqual(va, vb) {
			return false, fmt.Sprintf("%s.%s: %q vs %q", path, k, va, vb)
		}
	}
	for _, k := range sortedKeys(fieldsB) {
		if _, ok := fieldsA[k]; ok {
			continue
		}
		if c.opts.AbsentIsZero && valueZeroish(fieldsB[k]) {
			continue
		}
		return false, fmt.Sprintf("%s.%s present only in b (%q)", path, k, fieldsB[k])
	}

	if !c.opts.IgnoreSectionOrder {
		if len(sectionsA) != len(sectionsB) {
			return false, fmt.Sprintf("%s: %d vs %d sections", path, len(sectionsA), len(sectionsB))
		}
		for i := range sectionsA {
			sa, sb := sectionsA[i], sectionsB[i]
			if !equalFold(sa.key, sb.key) {
				return false, fmt.Sprintf("%s: section %d is [%s] vs [%s]", path, i, sa.key, sb.key)
			}
			if ok, msg := c.elements(sa.children, sb.children, path+"/"+foldKey(sa.key)); !ok {
				return false, msg
			}
		}
		return true, ""
	}

	byNameA, byNameB := byName(sectionsA), byName(sectionsB)
	for _, k := range sortedKeys(byNameA) {
		if ok, msg := c.sectionList(byNameA[k], byNameB[k], path+"/"+k); !ok {
			return false, msg
		}
	}
	for _, k := range sortedKeys(byNameB) {
		if _, ok := byNameA[k]; ok {
			continue
		}
		if ok, msg := c.sectionList(nil, byNameB[k], path+"/"+k); !ok {
			return false, msg
		}
	}
	return true, ""
}

// group returns a section's effective field values (the last assignment of
// each key, keys folded) and its child sections in order.
func group(els []*element) (fields map[string]string, sections []*element) {
	fields = map[string]string{}
	for _, el := range els {
		if el.section {
			sections = append(sections, el)
		} else {
			fields[foldKey(el.key)] = el.value
		}
	}
	return fields, sections
}

func byName(sections []*element) map[string][]*element {
	out := map[string][]*element{}
	for _, s := range sections {
		k := foldKey(s.key)
		out[k] = append(out[k], s)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c comparer) sectionList(a, b []*element, path string) (bool, string) {
	if len(a) == 0 || len(b) == 0 {
		extra := a
		if len(a) == 0 {
			extra = b
		}
		for _, el := range extra {
			if !c.opts.AbsentIsZero || !sectionZeroish(el) {
				return false, fmt.Sprintf("%s: section present on only one side", path)
			}
		}
		return true, ""
	}
	if len(a) != len(b) {
		return false, fmt.Sprintf("%s: %d vs %d sections", path, len(a), len(b))
	}
	for i := range a {
		if ok, msg := c.elements(a[i].children, b[i].children, path); !ok {
			return false, msg
		}
	}
	return true, ""
}

func (c comparer) valueEqual(a, b string) bool {
	if a == b {
		return true
	}
	if c.opts.CollapseSpace {
		a, b = collapseSpaces(a), collapseSpaces(b)
	}
	if c.opts.FoldValueCase {
		if equalFold(a, b) {
			return true
		}
	} else if a == b {
		return true
	}
	return numeralsEqual(a, b)
}

func collapseSpaces(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r < 0x80 && isSeparator(byte(r)) }), " ")
}

func valueZeroish(v string) bool {
	return v == "" || (isNumeral(v) && Atof(v) == 0)
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
