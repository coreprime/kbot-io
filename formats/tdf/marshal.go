package tdf

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Marshal renders v as TDF text. v must be a struct, a slice of structs, or a
// pointer to either. A slice produces one top-level [section] per element
// (named by its `tdf:",name"` field); a struct produces its tagged fields and
// nested sections at the top level. The inverse of Unmarshal.
//
// A zero field tagged omitempty is left out unless the struct's Meta records
// the key as present; a nil pointer or nil map is always left out. With a Meta,
// unchanged values keep their source text and keys and sections their source
// order and spelling (see Meta). Without one, fields are written in struct
// order, then catch-all entries sorted by key.
//
// Marshal refuses a key, value or section name the game would not read back
// unchanged (see CheckKey, CheckValue and CheckName), NaN, and integers outside
// the 32-bit range the game reads.
func Marshal(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("tdf: Marshal of nil %T", v)
		}
		rv = rv.Elem()
	}

	var els []*element
	switch rv.Kind() {
	case reflect.Slice:
		for i := 0; i < rv.Len(); i++ {
			el, err := encodeElement(rv.Index(i))
			if err != nil {
				return nil, err
			}
			els = append(els, el)
		}
	case reflect.Struct:
		children, err := encodeStruct(rv)
		if err != nil {
			return nil, err
		}
		els = children
	default:
		return nil, fmt.Errorf("tdf: Marshal requires a struct or slice, got %s", rv.Kind())
	}

	var b strings.Builder
	if err := writeElems(&b, els, 0); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// encodeElement turns a struct value into a named [section] element.
func encodeElement(rv reflect.Value) (*element, error) {
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	spec := specFor(rv.Type())
	name := ""
	if spec.nameIndex != nil {
		name = rv.FieldByIndex(spec.nameIndex).String()
	}
	children, err := encodeStruct(rv)
	if err != nil {
		return nil, err
	}
	return &element{key: name, section: true, children: children}, nil
}

// structMeta returns the struct's Meta, or nil when it has none.
func structMeta(rv reflect.Value, spec structSpec) *Meta {
	if spec.metaIndex == nil {
		return nil
	}
	f := rv.FieldByIndex(spec.metaIndex)
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return nil
		}
		return f.Interface().(*Meta)
	}
	m := f.Interface().(Meta)
	return &m
}

// encoder is the per-struct state of encodeStruct.
type encoder struct {
	meta      *Meta
	remaining reflect.Value
	remKeys   map[string]string // folded key -> key in remaining
	consumed  map[string]bool   // folded remaining keys a typed field handled
}

// encodeStruct renders a struct's tagged fields to an ordered element list.
func encodeStruct(rv reflect.Value) ([]*element, error) {
	spec := specFor(rv.Type())
	enc := &encoder{meta: structMeta(rv, spec), consumed: map[string]bool{}}
	if spec.remainingIndex != nil {
		enc.remaining = rv.FieldByIndex(spec.remainingIndex)
		enc.remKeys = map[string]string{}
		if !enc.remaining.IsNil() {
			for _, k := range enc.remaining.MapKeys() {
				enc.remKeys[foldKey(k.String())] = k.String()
			}
		}
	}
	var out []*element

	for _, fs := range spec.fields {
		if fs.isName || fs.isRemaining || fs.isSections {
			continue
		}
		f := rv.FieldByIndex(fs.index)
		switch cat := categorize(f.Type()); cat {
		case catScalar, catScalarList:
			el, err := enc.scalar(fs, f, cat == catScalarList)
			if err != nil {
				return nil, err
			}
			if el != nil {
				out = append(out, el)
			}
		case catSection:
			if f.Kind() == reflect.Pointer && f.IsNil() {
				continue
			}
			sv := f
			if f.Kind() == reflect.Pointer {
				sv = f.Elem()
			}
			children, err := encodeStruct(sv)
			if err != nil {
				return nil, err
			}
			if fs.omitempty && len(children) == 0 && !enc.meta.HasSection(fs.key) {
				continue
			}
			out = append(out, &element{key: enc.sectionName(fs.key), section: true, children: children})
		case catRepeated:
			// A repeats= field emits a sibling count key (e.g. SCHEMACOUNT=2)
			// ahead of the blocks, matching the on-disk layout.
			if fs.countKey != "" {
				key := fs.countKey
				if e := enc.meta.field(key); e != nil {
					key = e.key
				}
				out = append(out, &element{key: key, value: strconv.Itoa(f.Len())})
			}
			for i := 0; i < f.Len(); i++ {
				el, err := encodeElement(f.Index(i))
				if err != nil {
					return nil, err
				}
				if el.key == "" {
					el.key = fmt.Sprintf("%s%d", fs.key, i)
				}
				out = append(out, el)
			}
		case catMap:
			if f.IsNil() {
				continue
			}
			var sub *Meta
			if i := enc.meta.sectionIndex(fs.key, 0); i >= 0 {
				sub = enc.meta.entries[i].sub
			}
			children, err := encodeMap(f, sub)
			if err != nil {
				return nil, err
			}
			if fs.omitempty && len(children) == 0 && !enc.meta.HasSection(fs.key) {
				continue
			}
			out = append(out, &element{key: enc.sectionName(fs.key), section: true, children: children})
		}
	}

	if spec.sectionsIndex != nil {
		f := rv.FieldByIndex(spec.sectionsIndex)
		for i := 0; i < f.Len(); i++ {
			el, err := encodeElement(f.Index(i))
			if err != nil {
				return nil, err
			}
			out = append(out, el)
		}
	}

	if enc.remaining.IsValid() && !enc.remaining.IsNil() {
		keys := enc.remaining.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, k := range keys {
			if enc.consumed[foldKey(k.String())] {
				continue
			}
			out = append(out, &element{key: k.String(), value: enc.remaining.MapIndex(k).String()})
		}
	}
	return orderByMeta(out, enc.meta), nil
}

// sectionName is the spelling to write for a single section: the source's
// when the Meta has it.
func (enc *encoder) sectionName(key string) string {
	if i := enc.meta.sectionIndex(key, 0); i >= 0 {
		return enc.meta.entries[i].key
	}
	return key
}

// scalar renders one scalar or scalar-list field, or returns nil when it is
// left out. A value still equal to the text it was read from (kept in the Meta
// or, without one, in the catch-all) is written as that text.
func (enc *encoder) scalar(fs fieldSpec, f reflect.Value, list bool) (*element, error) {
	key := fs.key
	var raw string
	hasRaw := false
	if e := enc.meta.field(fs.key); e != nil {
		key = e.key
		raw, hasRaw = e.raw, e.hasRaw
	}
	if rk, ok := enc.remKeys[fs.ukey]; ok {
		enc.consumed[fs.ukey] = true
		if !hasRaw {
			key, raw, hasRaw = rk, enc.remaining.MapIndex(reflect.ValueOf(rk)).String(), true
		}
	}
	if hasRaw {
		same, parsed := decodesTo(f, raw, list, fs.delimiter)
		// Text that does not parse at all can only have come from a type
		// that parses itself; while its field is still zero, keep the text.
		if same || (!parsed && f.IsZero()) {
			return &element{key: key, value: raw}, nil
		}
	}
	if f.Kind() == reflect.Pointer && f.IsNil() {
		return nil, nil
	}
	present := enc.meta.Present(fs.key)
	if list {
		if fs.omitempty && f.Len() == 0 && !present {
			return nil, nil
		}
		v, err := encodeScalarList(f, fs.delimiter)
		if err != nil {
			return nil, err
		}
		return &element{key: key, value: v}, nil
	}
	s, zero, err := getScalar(f)
	if err != nil {
		return nil, fmt.Errorf("%w (key %s)", err, fs.key)
	}
	if fs.omitempty && zero && !present {
		return nil, nil
	}
	return &element{key: key, value: s}, nil
}

// encodeMap renders a map-typed section. With a Meta for the section, keys it
// recorded come first in source order and unchanged values keep their text;
// other keys follow sorted.
func encodeMap(f reflect.Value, sub *Meta) ([]*element, error) {
	keys := f.MapKeys()
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	out := make([]*element, 0, len(keys))
	for _, k := range keys {
		v := f.MapIndex(k)
		if e := sub.field(k.String()); e != nil && e.hasRaw {
			if same, _ := decodesTo(v, e.raw, false, ""); same {
				out = append(out, &element{key: k.String(), value: e.raw})
				continue
			}
		}
		s, _, err := getScalar(v)
		if err != nil {
			return nil, err
		}
		out = append(out, &element{key: k.String(), value: s})
	}
	return orderByMeta(out, sub), nil
}

// orderByMeta sorts elements into the order m recorded: fields by the
// position of their key, the n-th section of a name by the position of the
// source's n-th section of that name. Elements m does not know keep their
// relative order after all the others.
func orderByMeta(els []*element, m *Meta) []*element {
	if m == nil || len(m.entries) == 0 || len(els) < 2 {
		return els
	}
	pos := make([]int, len(els))
	seen := map[string]int{}
	for i, el := range els {
		var p int
		if el.section {
			u := foldKey(el.key)
			p = m.sectionIndex(el.key, seen[u])
			seen[u]++
		} else {
			p = m.fieldIndex(el.key)
		}
		if p < 0 {
			p = len(m.entries) + i
		}
		pos[i] = p
	}
	idx := make([]int, len(els))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return pos[idx[a]] < pos[idx[b]] })
	out := make([]*element, len(els))
	for i, j := range idx {
		out[i] = els[j]
	}
	return out
}
