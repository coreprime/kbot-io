// Package values holds the helpers the game-data packages share to read a
// struct field the way the game does: whether its key was present, the text it
// was read from, and the width the game stores it in.
package values

import (
	"math"
	"strconv"
	"strings"

	"github.com/coreprime/kbot-io/formats/tdf"
)

// Present reports whether a key counts as present in a struct: the source had
// it (or Meta.Mark added it), or its field holds a non-zero value, which
// Marshal writes whatever the Meta says.
func Present(m *tdf.Meta, key string, nonZero bool) bool {
	return nonZero || m.Present(key)
}

// Lookup finds key in a catch-all map, comparing ASCII case-insensitively.
func Lookup(rem map[string]string, key string) (string, bool) {
	if v, ok := rem[key]; ok {
		return v, true
	}
	for k, v := range rem {
		if EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// EqualFold compares two names ignoring ASCII case only, as the game does.
func EqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'a' <= x && x <= 'z' {
			x -= 'a' - 'A'
		}
		if 'a' <= y && y <= 'z' {
			y -= 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// HasPrefixFold reports whether s starts with prefix, ignoring ASCII case.
func HasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && EqualFold(s[:len(prefix)], prefix)
}

// IntText returns the text an int field was read from (from the Meta, or from
// the catch-all where a setter stored it), provided that text still reads as
// the field's current value. A field changed since it was read has no text.
func IntText(m *tdf.Meta, rem map[string]string, key string, cur int) (string, bool) {
	if raw, ok := m.Raw(key); ok {
		if int(tdf.Atol(raw)) == cur {
			return raw, true
		}
		return "", false
	}
	if raw, ok := Lookup(rem, key); ok && int(tdf.Atol(raw)) == cur {
		return raw, true
	}
	return "", false
}

// FloatText is IntText for a float64 field.
func FloatText(m *tdf.Meta, rem map[string]string, key string, cur float64) (string, bool) {
	if raw, ok := m.Raw(key); ok {
		if tdf.Atof(raw) == cur {
			return raw, true
		}
		return "", false
	}
	if raw, ok := Lookup(rem, key); ok && tdf.Atof(raw) == cur {
		return raw, true
	}
	return "", false
}

// Float returns the value the game reads, as a double, from a key an int field
// holds: the text's Atof value while the field is unchanged, else the field.
func Float(m *tdf.Meta, rem map[string]string, key string, cur int) float64 {
	if t, ok := IntText(m, rem, key, cur); ok {
		return tdf.Atof(t)
	}
	return float64(cur)
}

// IntOfFloat returns the value the game reads, as an integer (Atol), from a
// key a float64 field holds: "321.9" is 321.
func IntOfFloat(m *tdf.Meta, rem map[string]string, key string, cur float64) int32 {
	if t, ok := FloatText(m, rem, key, cur); ok {
		return tdf.Atol(t)
	}
	return truncInt32(cur)
}

// FixedOfFloat returns the game's 16.16 fixed-point reading of a key a float64
// field holds, as a float: the text's Fixed value while the field is
// unchanged, else the field's value quantised the same way.
func FixedOfFloat(m *tdf.Meta, rem map[string]string, key string, cur float64) float64 {
	if t, ok := FloatText(m, rem, key, cur); ok {
		return float64(tdf.Fixed(t)) / 65536
	}
	return float64(tdf.Fixed(strconv.FormatFloat(cur, 'f', -1, 64))) / 65536
}

// truncInt32 converts like the game's C conversion of a double to a 32-bit
// integer, giving 0 where that is undefined.
func truncInt32(v float64) int32 {
	if math.IsNaN(v) || v <= -2147483649 || v >= 2147483648 {
		return 0
	}
	return int32(v)
}

// SetText records text as the value of key for Marshal: the Meta forgets the
// key's source text and the catch-all holds the new text, which Marshal
// writes while the typed field still reads as it (so the typed field must be
// set to the value the codec reads from text). The key moves to the end of
// its section.
func SetText(m *tdf.Meta, rem *map[string]string, key, text string) {
	m.Forget(key)
	if *rem == nil {
		*rem = map[string]string{}
	}
	for k := range *rem {
		if EqualFold(k, key) {
			delete(*rem, k)
		}
	}
	(*rem)[key] = text
}

// SetFloat stores v for a key an int field holds: the field gets the value the
// codec reads from v's text (its integer part) and the text is kept so Marshal
// writes v exactly.
func SetFloat(m *tdf.Meta, rem *map[string]string, key string, field *int, v float64) {
	text := FormatFloat(v)
	*field = int(tdf.Atol(text))
	SetText(m, rem, key, text)
}

// FormatFloat writes v the way the game's Atof reads it back exactly.
func FormatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "1e999"
	case math.IsInf(v, -1):
		return "-1e999"
	case math.IsNaN(v):
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Truncate returns s cut to the first max bytes, as a fixed-size buffer in the
// game holds it.
func Truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// ListText returns the text a list field was read from while it still reads
// as cur with the codec's list rule for delim (see tdf.Unmarshal); cur is the
// list's elements formatted as the codec reads them.
func ListText(m *tdf.Meta, rem map[string]string, key, delim string, cur []int) (string, bool) {
	same := func(raw string) bool {
		got := SplitInts(raw, delim)
		if len(got) != len(cur) {
			return false
		}
		for i := range got {
			if got[i] != cur[i] {
				return false
			}
		}
		return true
	}
	if raw, ok := m.Raw(key); ok {
		if same(raw) {
			return raw, true
		}
		return "", false
	}
	if raw, ok := Lookup(rem, key); ok && same(raw) {
		return raw, true
	}
	return "", false
}

// SplitInts reads a list value into integers the way the tdf codec does for an
// []int field with the given delimiter.
func SplitInts(value, delim string) []int {
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
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i] = int(tdf.Atol(p))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
