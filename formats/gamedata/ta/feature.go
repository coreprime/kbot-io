package ta

import (
	"fmt"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
)

// Feature is one entry of a features/*.tdf file. Each feature is a top-level
// [SECTION]; decode a file with
//
//	var features []ta.Feature
//	err := tdf.Unmarshal(data, &features)
//
// Fields shared with TA:Kingdoms live on the embedded common.FeatureBase; the
// fields below are unique to Total Annihilation. A map's feature names the
// first section with that name (ignoring case) across the features files.
type Feature struct {
	common.FeatureBase

	AutoReclaimable int `tdf:"autoreclaimable,omitempty"`
	NoDrawUndergray int `tdf:"nodrawundergray,omitempty"`
	Geothermal      int `tdf:"geothermal,omitempty"`
}

// Feature satisfies the shared common.Feature interface via its embedded base.
var _ common.Feature = (*Feature)(nil)

const (
	// FeatureDescriptionMax is the longest feature description the game
	// keeps.
	FeatureDescriptionMax = 19
	// FeatureNameMax is the longest feature name the game keeps.
	FeatureNameMax = 127
)

// EffectiveAutoReclaimable reports whether builders reclaim the feature on
// their own: bit 0 of AutoReclaimable, or true when the key is missing (an
// explicit 0 is false).
func (f *Feature) EffectiveAutoReclaimable() bool {
	if values.Present(&f.Meta, "autoreclaimable", f.AutoReclaimable != 0) {
		return f.AutoReclaimable&1 != 0
	}
	return true
}

// EffectiveMetal returns the metal the feature yields as the game stores it:
// a whole number (Atol) kept to 16 bits, so 56.8 is 56 and 70000 is 4464.
func (f *Feature) EffectiveMetal() int {
	return int(uint16(values.IntOfFloat(&f.Meta, f.Remaining, "metal", f.Metal)))
}

// EffectiveEnergy returns the energy the feature yields, read like
// EffectiveMetal.
func (f *Feature) EffectiveEnergy() int {
	return int(uint16(values.IntOfFloat(&f.Meta, f.Remaining, "energy", f.Energy)))
}

// EffectiveHeight returns Height as the game stores it, kept to 8 bits,
// signed.
func (f *Feature) EffectiveHeight() int {
	return int(int8(f.Height))
}

// CheckFeatures reports what the game reads differently in one features file:
// descriptions and names longer than it keeps, metal or energy outside the 16
// bits it keeps, a section with no name and a second section of a name (maps
// use the first).
func CheckFeatures(features []Feature) []common.Warning {
	var out []common.Warning
	seen := map[string]bool{}
	for i := range features {
		f := &features[i]
		warn := func(key, format string, args ...any) {
			out = append(out, common.Warning{Section: f.Key, Key: key, Message: fmt.Sprintf(format, args...)})
		}
		if f.Key == "" {
			warn("", "the section has no name, so no map can place it")
		}
		folded := foldName(f.Key)
		if seen[folded] {
			warn("", "a second feature of this name; maps use the first")
		}
		seen[folded] = true
		if len(f.Key) > FeatureNameMax {
			warn("", "the name is %d characters; the game keeps the first %d", len(f.Key), FeatureNameMax)
		}
		if len(f.Description) > FeatureDescriptionMax {
			warn("description", "%d characters; the game keeps the first %d (%q)",
				len(f.Description), FeatureDescriptionMax, values.Truncate(f.Description, FeatureDescriptionMax))
		}
		for _, r := range []struct {
			key string
			v   float64
		}{{"metal", f.Metal}, {"energy", f.Energy}} {
			n := values.IntOfFloat(&f.Meta, f.Remaining, r.key, r.v)
			if n < 0 || n > 0xffff {
				warn(r.key, "%d is outside 0..65535; the game keeps the low 16 bits (%d)", n, uint16(n))
			}
		}
	}
	return out
}
