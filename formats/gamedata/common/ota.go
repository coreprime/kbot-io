package common

import (
	"strconv"
	"strings"

	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// GlobalHeaderBase is the set of [GlobalHeader] fields common to TA and
// TA:Kingdoms .ota map/mission files. Game-specific GlobalHeader types embed it
// and add their own fields (TA's Schema list, TA:Kingdoms' single Map Data
// block and kingdom metadata).
//
// Numeric fields hold the integer the codec reads (Atol). TA 3.1c reads
// tidalstrength, killmul and timemul as fractions; the source text is kept in
// Meta, so a value such as "18.5" survives a round trip, and the ta package's
// Effective accessors return the fraction. SetTidalStrength, SetKillMul and
// SetTimeMul write a fraction.
type GlobalHeaderBase struct {
	Key string `tdf:",name"` // section header, always GlobalHeader

	MissionName        string `tdf:"missionname,omitempty"`
	MissionDescription string `tdf:"missiondescription,omitempty"`
	Size               string `tdf:"size,omitempty"`
	Memory             string `tdf:"memory,omitempty"`
	UseOnlyUnits       string `tdf:"useonlyunits,omitempty"`

	LineOfSight int `tdf:"lineofsight,omitempty"`
	Mapping     int `tdf:"mapping,omitempty"`
	LavaWorld   int `tdf:"lavaworld,omitempty"`

	TidalStrength int `tdf:"tidalstrength,omitempty"`
	SolarStrength int `tdf:"solarstrength,omitempty"`
	MinWindSpeed  int `tdf:"minwindspeed,omitempty"`
	MaxWindSpeed  int `tdf:"maxwindspeed,omitempty"`
	Gravity       int `tdf:"gravity,omitempty"`

	KillMul int `tdf:"killmul,omitempty"`
	TimeMul int `tdf:"timemul,omitempty"`

	MaxUnits int `tdf:"maxunits,omitempty"`

	// NumPlayers holds the numbers of the comma-separated numplayers text
	// ("2, 3, 4"), each read with Atol, so text such as "2 3 4" or "2-8"
	// reads as a single 2. The game never parses numplayers: it shows the
	// text in the lobby. Use NumPlayersText for that text, which is kept
	// byte for byte, and PlayerCounts for a lenient reading of it.
	NumPlayers []int `tdf:"numplayers,omitempty,delimiter=', '"`

	WaterDoesDamage int `tdf:"waterdoesdamage,omitempty"`
	WaterDamage     int `tdf:"waterdamage,omitempty"`

	// Remaining preserves every other key=value (per-player setup, victory and
	// trigger conditions) so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves the header's child sections that no typed field
	// takes, in order, so the file round-trips.
	Sections []Section `tdf:",sections"`

	// Meta records which keys and sections the source had, in what order and
	// with what text (see tdf.Meta). It keeps explicit zeros, fractions and
	// the header's own spelling across a round trip.
	Meta tdf.Meta `tdf:",meta"`
}

// GlobalHeader is the read interface satisfied by every game's [GlobalHeader]
// type via its embedded GlobalHeaderBase.
type GlobalHeader interface {
	GetKey() string
	GetMissionName() string
	GetMissionDescription() string
	GetSize() string
	GetMemory() string
	GetUseOnlyUnits() string
	GetLineOfSight() int
	GetMapping() int
	GetLavaWorld() int
	GetTidalStrength() int
	GetSolarStrength() int
	GetMinWindSpeed() int
	GetMaxWindSpeed() int
	GetGravity() int
	GetKillMul() int
	GetTimeMul() int
	GetMaxUnits() int
	GetNumPlayers() []int
	GetWaterDoesDamage() int
	GetWaterDamage() int
	GetRemaining() map[string]string
}

func (b *GlobalHeaderBase) GetKey() string                  { return b.Key }
func (b *GlobalHeaderBase) GetMissionName() string          { return b.MissionName }
func (b *GlobalHeaderBase) GetMissionDescription() string   { return b.MissionDescription }
func (b *GlobalHeaderBase) GetSize() string                 { return b.Size }
func (b *GlobalHeaderBase) GetMemory() string               { return b.Memory }
func (b *GlobalHeaderBase) GetUseOnlyUnits() string         { return b.UseOnlyUnits }
func (b *GlobalHeaderBase) GetLineOfSight() int             { return b.LineOfSight }
func (b *GlobalHeaderBase) GetMapping() int                 { return b.Mapping }
func (b *GlobalHeaderBase) GetLavaWorld() int               { return b.LavaWorld }
func (b *GlobalHeaderBase) GetTidalStrength() int           { return b.TidalStrength }
func (b *GlobalHeaderBase) GetSolarStrength() int           { return b.SolarStrength }
func (b *GlobalHeaderBase) GetMinWindSpeed() int            { return b.MinWindSpeed }
func (b *GlobalHeaderBase) GetMaxWindSpeed() int            { return b.MaxWindSpeed }
func (b *GlobalHeaderBase) GetGravity() int                 { return b.Gravity }
func (b *GlobalHeaderBase) GetKillMul() int                 { return b.KillMul }
func (b *GlobalHeaderBase) GetTimeMul() int                 { return b.TimeMul }
func (b *GlobalHeaderBase) GetMaxUnits() int                { return b.MaxUnits }
func (b *GlobalHeaderBase) GetNumPlayers() []int            { return b.NumPlayers }
func (b *GlobalHeaderBase) GetWaterDoesDamage() int         { return b.WaterDoesDamage }
func (b *GlobalHeaderBase) GetWaterDamage() int             { return b.WaterDamage }
func (b *GlobalHeaderBase) GetRemaining() map[string]string { return b.Remaining }

const numPlayersDelimiter = ", "

// NumPlayersText returns the numplayers text as the lobby shows it: the
// source's text while NumPlayers is unchanged since it was read (or set with
// SetNumPlayersText), otherwise NumPlayers joined with ", ".
func (b *GlobalHeaderBase) NumPlayersText() string {
	if t, ok := values.ListText(&b.Meta, b.Remaining, "numplayers", numPlayersDelimiter, b.NumPlayers); ok {
		return t
	}
	parts := make([]string, len(b.NumPlayers))
	for i, n := range b.NumPlayers {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, numPlayersDelimiter)
}

// SetNumPlayersText sets numplayers to text, which Marshal writes unchanged.
// NumPlayers gets the numbers the codec reads from it. The key moves to the end
// of the header when written.
func (b *GlobalHeaderBase) SetNumPlayersText(text string) {
	b.NumPlayers = values.SplitInts(text, numPlayersDelimiter)
	values.SetText(&b.Meta, &b.Remaining, "numplayers", text)
}

// PlayerCounts reads the numplayers text leniently, for tools that want the
// player counts a map advertises: every number in it, in order, with "a-b"
// (or "a to b") expanded to the numbers from a to b. Numbers below 1 and
// repeats are dropped, and ranges are limited to 1..10. "2, 3, 4", "2 3 4" and
// "2-4" all give [2 3 4]; "Any" gives none. The game itself only shows the
// text.
func (b *GlobalHeaderBase) PlayerCounts() []int {
	return ParsePlayerCounts(b.NumPlayersText())
}

// maxPlayers is the largest player count TA offers.
const maxPlayers = 10

// ParsePlayerCounts is the lenient numplayers reading PlayerCounts applies.
func ParsePlayerCounts(text string) []int {
	var out []int
	seen := map[int]bool{}
	add := func(n int) {
		if n >= 1 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	lower := strings.ToLower(text)
	i := 0
	prev := -1   // last number read
	rng := false // a range marker followed prev
	for i < len(lower) {
		c := lower[i]
		switch {
		case c >= '0' && c <= '9':
			j := i
			for j < len(lower) && lower[j] >= '0' && lower[j] <= '9' {
				j++
			}
			n, err := strconv.Atoi(lower[i:j])
			if err != nil {
				n = -1
			}
			if rng && prev >= 0 && n >= prev {
				for k := prev + 1; k <= n && k <= maxPlayers; k++ {
					add(k)
				}
			}
			add(n)
			prev, rng = n, false
			i = j
		case c == '-':
			rng = prev >= 0
			i++
		case strings.HasPrefix(lower[i:], "to") && (i == 0 || !isLetter(lower[i-1])) &&
			(i+2 == len(lower) || !isLetter(lower[i+2])):
			rng = prev >= 0
			i += 2
		case c == ' ' || c == '\t':
			i++
		default:
			rng = false
			i++
		}
	}
	return out
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// SetTidalStrength sets tidalstrength to v, which may be fractional: Marshal
// writes it exactly and TidalStrength holds its integer part.
func (b *GlobalHeaderBase) SetTidalStrength(v float64) {
	values.SetFloat(&b.Meta, &b.Remaining, "tidalstrength", &b.TidalStrength, v)
}

// SetKillMul sets killmul to v, which may be fractional (see SetTidalStrength).
func (b *GlobalHeaderBase) SetKillMul(v float64) {
	values.SetFloat(&b.Meta, &b.Remaining, "killmul", &b.KillMul, v)
}

// SetTimeMul sets timemul to v, which may be fractional (see SetTidalStrength).
func (b *GlobalHeaderBase) SetTimeMul(v float64) {
	values.SetFloat(&b.Meta, &b.Remaining, "timemul", &b.TimeMul, v)
}
