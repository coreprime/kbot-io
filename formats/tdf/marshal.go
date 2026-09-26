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
// Fields are written in struct order, then catch-all entries sorted by key. A
// value whose source text Unmarshal kept in the catch-all is written as that
// text while it still reads as the field's value.
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

// encoder is the per-struct state of encodeStruct.
type encoder struct {
	remaining reflect.Value
	remKeys   map[string]string // folded key -> key in remaining
	consumed  map[string]bool   // folded remaining keys a typed field handled
}

// encodeStruct renders a struct's tagged fields to an ordered element list.
func encodeStruct(rv reflect.Value) ([]*element, error) {
	spec := specFor(rv.Type())
	enc := &encoder{consumed: map[string]bool{}}
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
			if fs.omitempty && len(children) == 0 {
				continue
			}
			out = append(out, &element{key: fs.key, section: true, children: children})
		case catRepeated:
			// A repeats= field emits a sibling count key (e.g. SCHEMACOUNT=2)
			// ahead of the blocks, matching the on-disk layout.
			if fs.countKey != "" {
				out = append(out, &element{key: fs.countKey, value: strconv.Itoa(f.Len())})
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
			children, err := encodeMap(f)
			if err != nil {
				return nil, err
			}
			if fs.omitempty && len(children) == 0 {
				continue
			}
			out = append(out, &element{key: fs.key, section: true, children: children})
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
	return out, nil
}

// scalar renders one scalar or scalar-list field, or returns nil when it is
// left out. A value still equal to the text it was read from (kept in the
// catch-all) is written as that text.
func (enc *encoder) scalar(fs fieldSpec, f reflect.Value, list bool) (*element, error) {
	key := fs.key
	var raw string
	hasRaw := false
	if rk, ok := enc.remKeys[fs.ukey]; ok {
		enc.consumed[fs.ukey] = true
		key, raw, hasRaw = rk, enc.remaining.MapIndex(reflect.ValueOf(rk)).String(), true
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
	if list {
		if fs.omitempty && f.Len() == 0 {
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
	if fs.omitempty && zero {
		return nil, nil
	}
	return &element{key: key, value: s}, nil
}

func encodeMap(f reflect.Value) ([]*element, error) {
	keys := f.MapKeys()
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	out := make([]*element, 0, len(keys))
	for _, k := range keys {
		s, _, err := getScalar(f.MapIndex(k))
		if err != nil {
			return nil, err
		}
		out = append(out, &element{key: k.String(), value: s})
	}
	return out, nil
}
