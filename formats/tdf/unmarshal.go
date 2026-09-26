package tdf

import (
	"fmt"
	"reflect"
	"strings"
)

// Unmarshal parses TDF/FBI/GUI bytes into v, which must be a non-nil pointer to
// a struct or to a slice of structs. It reads the text as the game does (see
// the package documentation) and repairs what the game would refuse; use
// UnmarshalWith for strict reading or to collect diagnostics.
//
// When v points to a slice, each top-level [section] becomes one element, in
// order and including sections with the same name; a field tagged
// `tdf:",name"` on the element struct receives the section header. When v
// points to a struct, the document's top-level sections and fields are matched
// against that struct's tagged fields.
//
// Field mapping by Go type:
//   - string / numeric / bool (and pointers to them): key=value, read with
//     the game's number rules (Atol, Atof, Flag); a nil pointer means the key
//     was absent
//   - []string / []int ...: a single space-separated value
//   - struct / *struct: a nested [name]{ } section
//   - []struct: repeated [name]{ } sections (matched by exact name, or by name
//     prefix when several share a stem like GADGET0, GADGET1)
//   - map[string]scalar: a section whose keys are dynamic (e.g. [DAMAGE])
//   - map[string]string tagged `,remaining`: catch-all for unmatched keys
//   - []struct tagged `,sections`: catch-all for unmatched child sections
//
// Keys and section names match ignoring ASCII case. A key assigned more than
// once keeps its last value, whatever the case of each assignment, as in the
// game. A struct or map field matching several sections takes the first, as
// the game's lookups do; the later ones go to the `,sections` catch-all when
// there is one. When a value's text would not be written back the same way
// (such as "13O", read as 13), the typed field gets the game's value and the
// text is also kept in the `,remaining` catch-all, so a round trip reproduces
// it.
func Unmarshal(data []byte, v any) error {
	return UnmarshalWith(data, v, ParseOptions{})
}

// UnmarshalWith is Unmarshal with explicit parse options.
func UnmarshalWith(data []byte, v any, opts ParseOptions) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("tdf: Unmarshal requires a non-nil pointer, got %T", v)
	}
	els, err := parseDocumentWith(data, opts)
	if err != nil {
		return err
	}
	target := rv.Elem()
	switch target.Kind() {
	case reflect.Slice:
		return decodeSlice(els, target)
	case reflect.Struct:
		return decodeStruct(els, target)
	default:
		return fmt.Errorf("tdf: Unmarshal target must point to a struct or slice, got %s", target.Kind())
	}
}

func decodeSlice(els []*element, target reflect.Value) error {
	for _, el := range els {
		if !el.section {
			continue
		}
		if err := appendRepeated(el, target); err != nil {
			return err
		}
	}
	return nil
}

// decodeState is the per-struct bookkeeping of one decodeStruct call.
type decodeState struct {
	rv        reflect.Value
	spec      structSpec
	remaining reflect.Value
	remKeys   map[string]string // folded key -> key as stored in remaining
	taken     map[int]bool      // indexes into spec.fields of single sections already filled
}

// decodeStruct fills rv (a struct) from a list of child elements.
func decodeStruct(children []*element, rv reflect.Value) error {
	st := &decodeState{rv: rv, spec: specFor(rv.Type()), taken: map[int]bool{}}
	if st.spec.remainingIndex != nil {
		st.remaining = rv.FieldByIndex(st.spec.remainingIndex)
		if st.remaining.IsNil() {
			st.remaining.Set(reflect.MakeMap(st.remaining.Type()))
		}
		st.remKeys = map[string]string{}
		for _, k := range st.remaining.MapKeys() {
			st.remKeys[foldKey(k.String())] = k.String()
		}
	}

	for _, child := range children {
		if child.section {
			if err := st.section(child); err != nil {
				return err
			}
			continue
		}
		if err := st.field(child); err != nil {
			return err
		}
	}
	return nil
}

// setRemaining stores key=value in the catch-all, replacing an earlier case
// variant of the key (the game keeps only the last assignment).
func (st *decodeState) setRemaining(key, value string) {
	u := foldKey(key)
	if old, ok := st.remKeys[u]; ok {
		key = old
	} else {
		st.remKeys[u] = key
	}
	st.remaining.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(value))
}

func (st *decodeState) dropRemaining(key string) {
	u := foldKey(key)
	if old, ok := st.remKeys[u]; ok {
		st.remaining.SetMapIndex(reflect.ValueOf(old), reflect.Value{})
		delete(st.remKeys, u)
	}
}

func (st *decodeState) field(child *element) error {
	if fs, ok := st.spec.fieldByName(child.key); ok {
		f := st.rv.FieldByIndex(fs.index)
		switch cat := categorize(f.Type()); cat {
		case catScalar, catScalarList:
			var err error
			if cat == catScalar {
				err = setScalar(f, child.value)
			} else {
				err = setScalarList(f, child.value, fs.delimiter)
			}
			if err != nil {
				// Only a type that parses itself can refuse a value. Keep
				// the text in the catch-all so the file still round-trips,
				// and leave the field zero, as if this assignment were its
				// text.
				if !st.remaining.IsValid() {
					return fmt.Errorf("tdf: field %s: %w", child.key, err)
				}
				f.Set(reflect.Zero(f.Type()))
				st.setRemaining(child.key, child.value)
				return nil
			}
			if st.remaining.IsValid() && cat == catScalar && !rendersAlike(f, child.value) {
				st.setRemaining(child.key, child.value)
			} else if st.remaining.IsValid() {
				st.dropRemaining(child.key)
			}
			return nil
		}
	}
	// A repeats= count key (e.g. SCHEMACOUNT) is derived from the slice length
	// on marshal, so drop it here instead of leaking it into the catch-all,
	// which would otherwise emit it twice.
	if st.spec.countKeys[foldKey(child.key)] {
		return nil
	}
	if st.remaining.IsValid() {
		st.setRemaining(child.key, child.value)
	}
	return nil
}

func (st *decodeState) section(child *element) error {
	u := foldKey(child.key)

	// Exact-name match for single sections and dynamic-key maps. The first
	// section of a name fills the field, as the game's lookups find the
	// first; later ones go to the catch-all.
	duplicate := false
	for i, fs := range st.spec.fields {
		if fs.isName || fs.isRemaining || fs.isSections || fs.ukey != u {
			continue
		}
		f := st.rv.FieldByIndex(fs.index)
		cat := categorize(f.Type())
		if cat != catSection && cat != catMap {
			continue
		}
		if st.taken[i] {
			duplicate = true
			break
		}
		st.taken[i] = true
		if cat == catSection {
			return decodeElement(child, sectionTarget(f))
		}
		return decodeMap(child, f)
	}

	// Prefix match for repeated section slices (e.g. GADGET0..GADGETn). A
	// keyless repeated field is the catch-all (handled below), not a prefix
	// match for every section, so require a non-empty key here.
	for _, fs := range st.spec.fields {
		if duplicate {
			break
		}
		if fs.isName || fs.isRemaining || fs.isSections || fs.ukey == "" {
			continue
		}
		f := st.rv.FieldByIndex(fs.index)
		if categorize(f.Type()) != catRepeated {
			continue
		}
		if strings.HasPrefix(u, fs.ukey) {
			return appendRepeated(child, f)
		}
	}
	// Unmatched (or repeated) child section: keep it in the ,sections
	// catch-all so the file round-trips, or drop it if the struct declares no
	// such field.
	if st.spec.sectionsIndex != nil {
		return appendRepeated(child, st.rv.FieldByIndex(st.spec.sectionsIndex))
	}
	return nil
}

// decodeElement sets a struct's name field (if any) and decodes its children.
func decodeElement(el *element, rv reflect.Value) error {
	spec := specFor(rv.Type())
	if spec.nameIndex != nil {
		rv.FieldByIndex(spec.nameIndex).SetString(el.key)
	}
	return decodeStruct(el.children, rv)
}

// sectionTarget returns an addressable struct value for a struct/*struct field.
func sectionTarget(f reflect.Value) reflect.Value {
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		return f.Elem()
	}
	return f
}

// decodeMap fills a map field from a section's fields. Case variants of a key
// merge into one entry holding the last value, spelled as first written.
func decodeMap(child *element, f reflect.Value) error {
	if f.IsNil() {
		f.Set(reflect.MakeMap(f.Type()))
	}
	vt := f.Type().Elem()
	keys := map[string]string{}
	for _, k := range f.MapKeys() {
		keys[foldKey(k.String())] = k.String()
	}
	for _, c := range child.children {
		if c.section {
			continue
		}
		ev := reflect.New(vt).Elem()
		if err := setScalar(ev, c.value); err != nil {
			return fmt.Errorf("tdf: map %s[%s]: %w", child.key, c.key, err)
		}
		key := c.key
		if old, ok := keys[foldKey(key)]; ok {
			key = old
		} else {
			keys[foldKey(key)] = key
		}
		f.SetMapIndex(reflect.ValueOf(key), ev)
	}
	return nil
}

func appendRepeated(child *element, f reflect.Value) error {
	et := f.Type().Elem()
	if et.Kind() == reflect.Pointer {
		ev := reflect.New(et.Elem())
		if err := decodeElement(child, ev.Elem()); err != nil {
			return err
		}
		f.Set(reflect.Append(f, ev))
		return nil
	}
	ev := reflect.New(et)
	if err := decodeElement(child, ev.Elem()); err != nil {
		return err
	}
	f.Set(reflect.Append(f, ev.Elem()))
	return nil
}
