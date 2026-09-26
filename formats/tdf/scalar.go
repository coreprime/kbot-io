package tdf

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// ScalarMarshaler lets a type render itself to a single TDF value (the right
// side of key=value). Implement it together with ScalarUnmarshaler on a type to
// have the codec treat it as a scalar field instead of a nested [section].
type ScalarMarshaler interface {
	MarshalTDF() (string, error)
}

// ScalarUnmarshaler lets a type parse itself from a single TDF value.
type ScalarUnmarshaler interface {
	UnmarshalTDF(string) error
}

var (
	scalarMarshalerType   = reflect.TypeOf((*ScalarMarshaler)(nil)).Elem()
	scalarUnmarshalerType = reflect.TypeOf((*ScalarUnmarshaler)(nil)).Elem()
)

// isCustomScalar reports whether t (or a pointer to t) implements the custom
// scalar codec interfaces, so a field of that type is encoded as one value
// rather than a nested section.
func isCustomScalar(t reflect.Type) bool {
	pt := reflect.PointerTo(t)
	return t.Implements(scalarMarshalerType) || t.Implements(scalarUnmarshalerType) ||
		pt.Implements(scalarMarshalerType) || pt.Implements(scalarUnmarshalerType)
}

// setScalar assigns a TDF value to a scalar (or pointer-to-scalar) field the
// way the game reads it: integers with Atol (wrapping to 32 bits, then keeping
// the low bits of narrower fields), floats with Atof and booleans with Flag
// (bit 0 of Atol). None of these fail. Only a type implementing
// ScalarUnmarshaler, which parses itself, can return an error.
func setScalar(f reflect.Value, s string) error {
	if f.Kind() != reflect.Pointer && f.CanAddr() {
		if u, ok := f.Addr().Interface().(ScalarUnmarshaler); ok {
			return u.UnmarshalTDF(s)
		}
	}
	switch f.Kind() {
	case reflect.Pointer:
		if f.IsNil() {
			f.Set(reflect.New(f.Type().Elem()))
		}
		if u, ok := f.Interface().(ScalarUnmarshaler); ok {
			return u.UnmarshalTDF(s)
		}
		return setScalar(f.Elem(), s)
	case reflect.String:
		f.SetString(s)
	case reflect.Bool:
		f.SetBool(Flag(s))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f.SetInt(int64(Atol(s)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f.SetUint(uint64(uint32(Atol(s))))
	case reflect.Float32, reflect.Float64:
		f.SetFloat(Atof(s))
	default:
		return fmt.Errorf("unsupported scalar kind %s", f.Kind())
	}
	return nil
}

// getScalar renders a scalar field to its TDF string form and reports whether
// the value is the type's zero value (used by omitempty). Types implementing
// ScalarMarshaler render themselves; a non-nil pointer is always treated as
// present so a pointer-to-custom field round-trips even when its value is the
// zero value (e.g. an RGBString of "0 0 0").
//
// A value the game cannot read back as the same number is an error: NaN, and
// integers outside the 32-bit range the game reads (signed kinds must fit an
// int32, unsigned ones a uint32). An infinite float is written as ±1e999, which
// the game reads back as the same infinity.
func getScalar(f reflect.Value) (string, bool, error) {
	if f.CanInterface() {
		if m, ok := f.Interface().(ScalarMarshaler); ok {
			if f.Kind() == reflect.Pointer && f.IsNil() {
				return "", true, nil
			}
			s, err := m.MarshalTDF()
			zero := f.Kind() != reflect.Pointer && f.IsZero()
			return s, zero, err
		}
	}
	switch f.Kind() {
	case reflect.Pointer:
		if f.IsNil() {
			return "", true, nil
		}
		s, _, err := getScalar(f.Elem())
		return s, false, err
	case reflect.String:
		s := f.String()
		return s, s == "", nil
	case reflect.Bool:
		if f.Bool() {
			return "1", false, nil
		}
		return "0", true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i := f.Int()
		if i < math.MinInt32 || i > math.MaxInt32 {
			return "", false, fmt.Errorf("tdf: integer %d is outside the 32-bit range the game reads", i)
		}
		return strconv.FormatInt(i, 10), i == 0, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := f.Uint()
		if u > math.MaxUint32 {
			return "", false, fmt.Errorf("tdf: integer %d is outside the 32-bit range the game reads", u)
		}
		return strconv.FormatUint(u, 10), u == 0, nil
	case reflect.Float32:
		s, err := formatFloat(f.Float(), 32)
		return s, f.Float() == 0, err
	case reflect.Float64:
		s, err := formatFloat(f.Float(), 64)
		return s, f.Float() == 0, err
	default:
		return "", true, nil
	}
}

func formatFloat(v float64, bits int) (string, error) {
	switch {
	case math.IsNaN(v):
		return "", fmt.Errorf("tdf: NaN has no TDF representation")
	case math.IsInf(v, 1):
		return "1e999", nil
	case math.IsInf(v, -1):
		return "-1e999", nil
	}
	return strconv.FormatFloat(v, 'f', -1, bits), nil
}

// setScalarList parses a single value into a slice. With an empty delim the
// value is split on runs of whitespace (the default). Otherwise it is split on
// the delimiter's non-space core (so "," and ", " both split "2, 3, 4"), with
// surrounding whitespace trimmed from each element.
func setScalarList(f reflect.Value, value, delim string) error {
	var parts []string
	if core := strings.TrimSpace(delim); core == "" {
		parts = strings.Fields(value)
	} else {
		for _, p := range strings.Split(value, core) {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
	}
	s := reflect.MakeSlice(f.Type(), len(parts), len(parts))
	for i, p := range parts {
		if err := setScalar(s.Index(i), p); err != nil {
			return err
		}
	}
	f.Set(s)
	return nil
}

// encodeScalarList joins a slice's elements with delim (a single space when
// delim is empty), reproducing the on-disk separator.
func encodeScalarList(f reflect.Value, delim string) (string, error) {
	parts := make([]string, f.Len())
	for i := range parts {
		s, _, err := getScalar(f.Index(i))
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	join := " "
	if delim != "" {
		join = delim
	}
	return strings.Join(parts, join), nil
}

// decodesTo reports whether raw, decoded into a fresh value of f's type, gives
// f's current value. It is how the encoder decides that a value is unchanged
// since it was read and can be written back as the text it was read from.
// parsed is false when raw does not decode at all.
func decodesTo(f reflect.Value, raw string, list bool, delim string) (same, parsed bool) {
	fresh := reflect.New(f.Type()).Elem()
	var err error
	if list {
		err = setScalarList(fresh, raw, delim)
	} else {
		err = setScalar(fresh, raw)
	}
	if err != nil {
		return false, false
	}
	return reflect.DeepEqual(fresh.Interface(), f.Interface()), true
}

// rendersAlike reports whether writing the value decoded from raw gives text
// SemanticEqual accepts as the same as raw. When it does not ("13O" is written
// back as "13", "2" in a bool as "0"), Unmarshal keeps raw in the ,remaining
// catch-all so the text survives a round trip.
func rendersAlike(f reflect.Value, raw string) bool {
	s, _, err := getScalar(f)
	if err != nil {
		return false
	}
	return s == raw || numeralsEqual(s, raw)
}
