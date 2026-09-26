package ta

import (
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// Meteor is one section of gamedata/meteor.tdf: a named meteor-shower weapon
// configuration (the stock file defines a single [Default], which the game
// uses when a map's schema leaves its meteor settings out; see
// Schema.Meteors). Decode the whole file with
//
//	var meteors []ta.Meteor
//	err := tdf.Unmarshal(data, &meteors)
//
// The game reads MeteorDensity, MeteorDuration and MeteorInterval as
// fractions; MeteorDuration and MeteorInterval hold only the whole part, and
// EffectiveMeteorDuration and EffectiveMeteorInterval give the fraction.
type Meteor struct {
	Key string `tdf:",name"` // section header, e.g. "Default"

	MeteorWeapon   string  `tdf:"meteorweapon,omitempty"`
	MeteorRadius   int     `tdf:"meteorradius,omitempty"`
	MeteorDensity  float64 `tdf:"meteordensity,omitempty"`
	MeteorDuration int     `tdf:"meteorduration,omitempty"`
	MeteorInterval int     `tdf:"meteorinterval,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records which keys the source had, in what order and with what
	// text, so fractions and explicit zeros survive a round trip.
	Meta tdf.Meta `tdf:",meta"`
}

// EffectiveMeteorDuration returns the meteor duration as the game reads it, a
// fraction where MeteorDuration holds only its whole part.
func (m *Meteor) EffectiveMeteorDuration() float64 {
	return values.Float(&m.Meta, m.Remaining, "meteorduration", m.MeteorDuration)
}

// EffectiveMeteorInterval returns the meteor interval as the game reads it, a
// fraction where MeteorInterval holds only its whole part.
func (m *Meteor) EffectiveMeteorInterval() float64 {
	return values.Float(&m.Meta, m.Remaining, "meteorinterval", m.MeteorInterval)
}

// SetMeteorDuration sets meteorduration to v, which may be fractional: Marshal
// writes it exactly and MeteorDuration holds its whole part.
func (m *Meteor) SetMeteorDuration(v float64) {
	values.SetFloat(&m.Meta, &m.Remaining, "meteorduration", &m.MeteorDuration, v)
}

// SetMeteorInterval sets meteorinterval to v (see SetMeteorDuration).
func (m *Meteor) SetMeteorInterval(v float64) {
	values.SetFloat(&m.Meta, &m.Remaining, "meteorinterval", &m.MeteorInterval, v)
}

// MeteorSettings is the meteor shower a game runs.
type MeteorSettings struct {
	// Enabled is false when the schema names no meteor weapon.
	Enabled  bool
	Weapon   string
	Radius   int
	Density  float64
	Duration float64
	Interval float64
}

// settings returns the section's values as meteor settings.
func (m *Meteor) settings() MeteorSettings {
	return MeteorSettings{
		Enabled:  m.MeteorWeapon != "",
		Weapon:   m.MeteorWeapon,
		Radius:   m.MeteorRadius,
		Density:  m.MeteorDensity,
		Duration: m.EffectiveMeteorDuration(),
		Interval: m.EffectiveMeteorInterval(),
	}
}
