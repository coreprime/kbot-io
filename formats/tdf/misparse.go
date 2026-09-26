package tdf

import (
	"fmt"
	"reflect"
)

// MisparsedKeys walks an already-decoded value and reports typed scalar fields
// whose source text does not fit their type: text such as "13O" or "2, 4" in a
// numeric field, "true" in a flag, or anything a custom scalar type refused.
// The game reads such text with its own number rules (Atol, Atof, Flag), which
// is what Unmarshal stores in the field; the text itself is kept in the
// struct's ",remaining" catch-all so the document still round-trips. Because the text is preserved, a byte-level round-trip
// check cannot reveal the mismatch, but it is almost always a mis-typed struct
// field or genuinely malformed game data. Empty values and plain numerals (such
// as "1.5" in an integer field) are not reported.
//
// Each result is formatted as "Type.key=value".
func MisparsedKeys(v any) []string {
	var out []string
	seen := map[string]bool{}
	walkMisparse(reflect.ValueOf(v), &out, seen)
	return out
}

// misfit reports whether raw is text worth reporting for a field.
func misfit(raw string) bool {
	return raw != "" && !isNumeral(raw)
}

func walkMisparse(rv reflect.Value, out *[]string, seen map[string]bool) {
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !rv.IsNil() {
			walkMisparse(rv.Elem(), out, seen)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			walkMisparse(rv.Index(i), out, seen)
		}
	case reflect.Map:
		for _, k := range rv.MapKeys() {
			walkMisparse(rv.MapIndex(k), out, seen)
		}
	case reflect.Struct:
		spec := specFor(rv.Type())
		if spec.remainingIndex != nil {
			rem := rv.FieldByIndex(spec.remainingIndex)
			if rem.Kind() == reflect.Map && !rem.IsNil() {
				id := rem.Pointer()
				for _, k := range rem.MapKeys() {
					name := k.String()
					if _, ok := spec.fieldByName(name); !ok {
						continue
					}
					raw := rem.MapIndex(k).String()
					if !misfit(raw) {
						continue
					}
					// The catch-all map is shared with embedded bases, so the
					// same (map, key) can be visited via several struct levels.
					dedup := fmt.Sprintf("%x|%s", id, name)
					if seen[dedup] {
						continue
					}
					seen[dedup] = true
					*out = append(*out, fmt.Sprintf("%s.%s=%s", rv.Type().Name(), name, raw))
				}
			}
		}
		for i := 0; i < rv.NumField(); i++ {
			f := rv.Type().Field(i)
			if f.PkgPath != "" && !f.Anonymous {
				continue
			}
			walkMisparse(rv.Field(i), out, seen)
		}
	}
}
