package tdf

import (
	"io"
	"os"
	"strconv"
	"strings"
)

// Document is a TDF text as a tree: its top-level fields and sections, each
// section holding fields and nested sections, all in the order they were read.
// Parse reads it with the same grammar as Unmarshal.
//
// Lookups ignore ASCII case, as the game's do. A key assigned more than once
// in a section is one Field holding the last value (the one the game uses),
// spelled and placed as first written. Sections with the same name are all
// kept, in order; Section returns the first, as the game's lookups do.
type Document struct {
	root  *Section
	diags []Diagnostic
}

// Section is a [name]{ ... } block of a Document, or the document's top level.
type Section struct {
	name  string
	items []item
	index map[string]*Field // folded key -> field
}

// item is one entry of a section: a field or a child section.
type item struct {
	field   *Field
	section *Section
}

// Field is a key=value pair of a section.
type Field struct {
	key   string
	value string
}

// Key returns the field key, spelled as first written.
func (f *Field) Key() string {
	return f.key
}

// Value returns the field value: its last assignment, trimmed of separators.
func (f *Field) Value() string {
	return f.value
}

func newSection(name string) *Section {
	return &Section{name: name, index: map[string]*Field{}}
}

// NewDocument creates a new empty TDF document.
func NewDocument() *Document {
	return &Document{root: newSection("")}
}

// Parse reads TDF text from r the way the game does, repairing what the game
// would refuse (see ParseOptions); Diagnostics lists what was found.
func Parse(r io.Reader) (*Document, error) {
	return ParseWith(r, ParseOptions{})
}

// ParseWith reads TDF text from r with explicit parse options.
func ParseWith(r io.Reader, opts ParseOptions) (*Document, error) {
	doc := NewDocument()
	report := opts.OnDiagnostic
	opts.OnDiagnostic = func(d Diagnostic) {
		doc.diags = append(doc.diags, d)
		if report != nil {
			report(d)
		}
	}
	p, err := newParser(r, opts)
	if err != nil {
		return nil, err
	}
	els, err := p.all()
	if err != nil {
		return nil, err
	}
	sortDiagnostics(doc.diags)
	doc.root.fill(els)
	return doc, nil
}

func (s *Section) fill(els []*element) {
	for _, el := range els {
		if el.section {
			child := newSection(el.key)
			child.fill(el.children)
			s.items = append(s.items, item{section: child})
			continue
		}
		s.Set(el.key, el.value)
	}
}

// ParseFile parses a TDF file.
func ParseFile(path string) (*Document, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	return Parse(file)
}

// ParseString parses TDF content from a string.
func ParseString(content string) (*Document, error) {
	return Parse(strings.NewReader(content))
}

// Diagnostics returns what Parse found in the text, in offset order:
// conditions the game refuses (and Parse repaired) and ones it accepts that
// are probably mistakes.
func (d *Document) Diagnostics() []Diagnostic {
	return d.diags
}

// Root returns the document's top level as a Section: fields written outside
// any [section], and the top-level sections. Its name is empty.
func (d *Document) Root() *Section {
	return d.root
}

// Fields returns the fields written outside any section, in order.
func (d *Document) Fields() []*Field {
	return d.root.Fields()
}

// Section returns the first top-level section with this name (ignoring ASCII
// case), or nil.
func (d *Document) Section(name string) *Section {
	return d.root.Section(name)
}

// Sections returns every top-level section in order, including sections that
// share a name.
func (d *Document) Sections() []*Section {
	return d.root.Sections()
}

// AddSection returns the first top-level section with this name, adding an
// empty one at the end when there is none.
func (d *Document) AddSection(name string) *Section {
	return d.root.AddSection(name)
}

// HasSection reports whether a top-level section with this name exists.
func (d *Document) HasSection(name string) bool {
	return d.root.Section(name) != nil
}

// Write writes the document as TDF text: top-level fields and sections in
// order, sections in the retail layout ("[NAME]", then the braces and fields
// indented one tab). Comments and the original spacing are not kept.
func (d *Document) Write(w io.Writer) error {
	e := &errWriter{w: &ioTokenWriter{w: w}}
	d.root.writeItems(e, 0, "\n")
	return e.err
}

// writeItems writes a section's items, each line ended by nl; depth is the
// indentation of its fields.
func (s *Section) writeItems(e *errWriter, depth int, nl string) {
	pad := strings.Repeat("\t", depth)
	for _, it := range s.items {
		if f := it.field; f != nil {
			e.str(pad)
			e.str(f.key)
			e.ch('=')
			e.str(f.value)
			e.ch(';')
			e.str(nl)
			continue
		}
		c := it.section
		e.str(pad)
		e.ch('[')
		e.str(c.name)
		e.ch(']')
		e.str(nl)
		e.str(pad)
		e.str("\t{")
		e.str(nl)
		c.writeItems(e, depth+1, nl)
		e.str(pad)
		e.str("\t}")
		e.str(nl)
	}
}

// ioTokenWriter adapts an io.Writer to tokenWriter.
type ioTokenWriter struct{ w io.Writer }

func (t *ioTokenWriter) WriteString(s string) (int, error) { return io.WriteString(t.w, s) }
func (t *ioTokenWriter) WriteByte(c byte) error {
	_, err := t.w.Write([]byte{c})
	return err
}

// WriteFile writes the document to a file.
func (d *Document) WriteFile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := d.Write(file); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// String returns the TDF content as a string.
func (d *Document) String() string {
	var sb strings.Builder
	if err := d.Write(&sb); err != nil {
		return ""
	}
	return sb.String()
}

// Name returns the section name ("" for a document's top level).
func (s *Section) Name() string {
	return s.name
}

// Get returns a field value by key (ignoring ASCII case), and whether the key
// is present. A key assigned more than once gives its last value.
func (s *Section) Get(key string) (string, bool) {
	if field, exists := s.index[foldKey(key)]; exists {
		return field.value, true
	}
	return "", false
}

// Set sets a field value. An existing field (matched ignoring ASCII case)
// keeps its key spelling and position; a new one is added after the section's
// other items.
func (s *Section) Set(key, value string) {
	if field, exists := s.index[foldKey(key)]; exists {
		field.value = value
		return
	}
	field := &Field{key: key, value: value}
	s.index[foldKey(key)] = field
	s.items = append(s.items, item{field: field})
}

// Delete removes a field (matched ignoring ASCII case) and reports whether it
// was present.
func (s *Section) Delete(key string) bool {
	u := foldKey(key)
	f, ok := s.index[u]
	if !ok {
		return false
	}
	delete(s.index, u)
	for i, it := range s.items {
		if it.field == f {
			s.items = append(s.items[:i], s.items[i+1:]...)
			break
		}
	}
	return true
}

// Has checks if a field exists.
func (s *Section) Has(key string) bool {
	_, exists := s.index[foldKey(key)]
	return exists
}

// String returns a string value (empty string if not found).
func (s *Section) String(key string) string {
	value, _ := s.Get(key)
	return value
}

// Int returns an integer value read the way the game reads it (see Atol): the
// leading digits after an optional sign, wrapped to 32 bits, so "12abc" is 12
// and "1.9" is 1. A missing key gives 0.
func (s *Section) Int(key string) int {
	value, _ := s.Get(key)
	return int(Atol(value))
}

// Float returns a floating-point value read the way the game reads it (see
// Atof), so "13O" is 13 and "1.5d2" is 150. A missing key gives 0.
func (s *Section) Float(key string) float64 {
	value, _ := s.Get(key)
	return Atof(value)
}

// Fixed returns a value as the game's 16.16 fixed point (see Fixed). A missing
// key gives 0.
func (s *Section) Fixed(key string) int32 {
	value, _ := s.Get(key)
	return Fixed(value)
}

// Bool returns a flag read the way the game reads it (see Flag): bit 0 of the
// integer value, so "1" and "3" are true while "0", "2", "true" and "yes" are
// false. A missing key gives false.
func (s *Section) Bool(key string) bool {
	value, _ := s.Get(key)
	return Flag(value)
}

// List returns a value split by spaces (for category lists, etc.)
func (s *Section) List(key string) []string {
	value, ok := s.Get(key)
	if !ok {
		return nil
	}
	return strings.Fields(value)
}

// SetString sets a string value
func (s *Section) SetString(key, value string) {
	s.Set(key, value)
}

// SetInt sets an integer value
func (s *Section) SetInt(key string, value int) {
	s.Set(key, strconv.Itoa(value))
}

// SetFloat sets a float value
func (s *Section) SetFloat(key string, value float64) {
	s.Set(key, strconv.FormatFloat(value, 'f', -1, 64))
}

// SetBool sets a boolean value (as 1 or 0)
func (s *Section) SetBool(key string, value bool) {
	if value {
		s.Set(key, "1")
	} else {
		s.Set(key, "0")
	}
}

// SetList sets a space-separated list value
func (s *Section) SetList(key string, values []string) {
	s.Set(key, strings.Join(values, " "))
}

// Fields returns the section's fields in order, one per key.
func (s *Section) Fields() []*Field {
	fields := make([]*Field, 0, len(s.index))
	for _, it := range s.items {
		if it.field != nil {
			fields = append(fields, it.field)
		}
	}
	return fields
}

// Sections returns the nested sections in order, including sections that
// share a name.
func (s *Section) Sections() []*Section {
	var out []*Section
	for _, it := range s.items {
		if it.section != nil {
			out = append(out, it.section)
		}
	}
	return out
}

// Section returns the first nested section with this name (ignoring ASCII
// case), or nil.
func (s *Section) Section(name string) *Section {
	for _, it := range s.items {
		if it.section != nil && equalFold(it.section.name, name) {
			return it.section
		}
	}
	return nil
}

// AddSection returns the first nested section with this name, adding an empty
// one after the section's other items when there is none.
func (s *Section) AddSection(name string) *Section {
	if c := s.Section(name); c != nil {
		return c
	}
	c := newSection(name)
	s.items = append(s.items, item{section: c})
	return c
}
