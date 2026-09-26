package tdf

// Meta records what the source text of one section said, beyond the values a
// struct holds: which keys and sections were present, in what order, with
// what spelling, and the exact text of each value.
//
// Add a field of type Meta tagged `tdf:",meta"` to a struct (an embedded base
// struct works too) and Unmarshal and Decoder fill it for every section decoded
// into that struct, while Marshal and Encoder use it:
//
//   - Presence. A key that was present is written even when its field holds
//     the zero value and is tagged omitempty, so an explicit "key=0;" survives
//     a round trip instead of being dropped (the game applies its own default,
//     often not zero, to a missing key). A key that was absent and whose field
//     is still zero stays absent. The same goes for sections: a section that
//     was present is written even when empty. Present reports which keys were
//     there; Mark and Forget change that before writing.
//   - Source text. A field whose value is unchanged since it was read (its text
//     still decodes to the field's current value) is written with exactly that
//     text, so "13O", ".6", "2" in a flag or a category list with double spaces
//     survive byte for byte. A changed value is written in the codec's normal
//     form.
//   - Order and spelling. Keys and sections are written in the order they were
//     read, with the key spelling of the source; keys the source did not have
//     follow in struct order. Entries of a map-typed section (such as
//     [DAMAGE]) keep their order and text too, and sections nested in it,
//     which the map cannot hold, are kept and written back unchanged.
//   - Origin. Decoded reports whether the Meta was filled from a source, so
//     Marshal can tell a decoded struct that lacked a key from one built in
//     code (see the repeats= tag option).
//
// Pointer fields give presence without Meta: a nil pointer is absent and a
// non-nil one is written whatever its value. Meta adds presence to plain
// fields so existing struct layouts keep their types.
//
// A Meta holds no values of its own that need to stay in step with the
// struct: a key whose field changes is written with its new value. The zero
// Meta is empty and ready to use. A copied Meta records the same source, and
// Mark or Forget on one copy leaves the other unchanged.
type Meta struct {
	entries []metaEntry
	fields  map[string]int // folded field key -> index into entries
	decoded bool           // filled by decoding a section
}

type metaEntry struct {
	key     string // spelling as written in the source
	raw     string // value text of the last assignment (fields)
	hasRaw  bool
	section bool
	sub     *Meta    // entries of a section decoded into a map
	el      *element // a section nested in a map-typed section, kept whole
}

// Decoded reports whether Unmarshal or a Decoder filled the Meta from a
// section of source text. It is false for the zero Meta of a struct built in
// code.
func (m *Meta) Decoded() bool {
	return m != nil && m.decoded
}

// Present reports whether the source had a field with this key (compared
// ignoring ASCII case), or whether Mark added it.
func (m *Meta) Present(key string) bool {
	if m == nil {
		return false
	}
	_, ok := m.fields[foldKey(key)]
	return ok
}

// HasSection reports whether the source had a section with this name
// (compared ignoring ASCII case).
func (m *Meta) HasSection(name string) bool {
	return m.sectionIndex(name, 0) >= 0
}

// Raw returns the text of the key's last assignment in the source, and whether
// there was one.
func (m *Meta) Raw(key string) (string, bool) {
	if m == nil {
		return "", false
	}
	i, ok := m.fields[foldKey(key)]
	if !ok || !m.entries[i].hasRaw {
		return "", false
	}
	return m.entries[i].raw, true
}

// Keys returns the field keys the source had, in source order, spelled as in
// the source. A key assigned more than once appears once, at its first
// position.
func (m *Meta) Keys() []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, e := range m.entries {
		if !e.section {
			out = append(out, e.key)
		}
	}
	return out
}

// Mark records key as present, so Marshal writes it even when its field holds
// the zero value. It has no effect on a key already present.
func (m *Meta) Mark(key string) {
	if m.Present(key) {
		return
	}
	// Copy before changing, so a copy of this Meta is unaffected.
	m.entries = append([]metaEntry(nil), m.entries...)
	m.reindex()
	m.addField(key)
}

// Forget removes key from the record, so Marshal treats it as never read: a
// zero omitempty field is left out again and a value is written in normal
// form.
func (m *Meta) Forget(key string) {
	if m == nil || m.fields == nil {
		return
	}
	i, ok := m.fields[foldKey(key)]
	if !ok {
		return
	}
	m.entries = append(m.entries[:i:i], m.entries[i+1:]...)
	m.reindex()
}

func (m *Meta) reindex() {
	m.fields = map[string]int{}
	for i, e := range m.entries {
		if !e.section {
			m.fields[foldKey(e.key)] = i
		}
	}
}

func (m *Meta) addField(key string) *metaEntry {
	if m.fields == nil {
		m.fields = map[string]int{}
	}
	m.entries = append(m.entries, metaEntry{key: key})
	m.fields[foldKey(key)] = len(m.entries) - 1
	return &m.entries[len(m.entries)-1]
}

// recordField notes a field read from the source: the first assignment fixes
// its position and spelling, the last its text.
func (m *Meta) recordField(key, raw string) {
	var e *metaEntry
	if i, ok := m.fields[foldKey(key)]; ok {
		e = &m.entries[i]
	} else {
		e = m.addField(key)
	}
	e.raw, e.hasRaw = raw, true
}

// recordSection notes a section read from the source and returns the index of
// its entry.
func (m *Meta) recordSection(name string) int {
	m.entries = append(m.entries, metaEntry{key: name, section: true})
	return len(m.entries) - 1
}

// sectionIndex returns the entry index of the n-th (0-based) section with
// this name, or -1.
func (m *Meta) sectionIndex(name string, n int) int {
	if m == nil {
		return -1
	}
	u := foldKey(name)
	for i, e := range m.entries {
		if e.section && foldKey(e.key) == u {
			if n == 0 {
				return i
			}
			n--
		}
	}
	return -1
}

// fieldIndex returns the entry index of a field key, or -1.
func (m *Meta) fieldIndex(key string) int {
	if m == nil {
		return -1
	}
	if i, ok := m.fields[foldKey(key)]; ok {
		return i
	}
	return -1
}

// field returns the entry of a field key, or nil.
func (m *Meta) field(key string) *metaEntry {
	if i := m.fieldIndex(key); i >= 0 {
		return &m.entries[i]
	}
	return nil
}
