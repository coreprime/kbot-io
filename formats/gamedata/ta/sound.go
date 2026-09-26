package ta

import (
	"strconv"

	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// SoundClass is one section of gamedata/sound.tdf: a named set of game-event to
// sound-sample mappings (e.g. [ARM_KBOT] { select1=kbarmsel; ok1=kbarmmov; }).
// The event keys are open-ended, so every key=value is captured in Events.
// Decode the whole file with
//
//	var classes []ta.SoundClass
//	err := tdf.Unmarshal(data, &classes)
//
// The game reads, for each of SoundEvents, the key named after the event and
// then the numbered keys EVENT1, EVENT2, ... up to the first missing number;
// Sounds returns them. Other keys are never played.
type SoundClass struct {
	Key string `tdf:",name"` // section header, e.g. "ARM_KBOT"

	// Events maps each game event (select1, ok1, underattack, ...) to its sound
	// sample. It captures every key in the section so the file round-trips.
	Events map[string]string `tdf:",remaining"`

	// Sections preserves any section nested in the class.
	Sections []common.Section `tdf:",sections"`

	// Meta records the order and text of the keys (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// SoundEvents lists the events the game reads from each sound class, in its
// order. The keys of an event are the name itself and the name followed by 1,
// 2, ...
var SoundEvents = []string{
	"select", "underattack", "activate", "deactivate", "ok", "arrived", "cant",
	"unitcomplete", "build", "repair", "working", "load", "unload", "cloak",
	"uncloak", "capture", "count5", "count4", "count3", "count2", "count1",
	"count0", "canceldestruct",
}

// SoundTextMax is the longest sound name, or text, the game keeps.
const SoundTextMax = 63

// SoundChoice is one sound the game may play for an event: the sample name
// and the text of the matching KEYtext key (empty when there is none). An
// empty Sound is a silent choice.
type SoundChoice struct {
	Sound string
	Text  string
}

// Sounds returns the choices the game has for event (one of SoundEvents,
// compared ignoring case) in this class: the value of the key named event,
// when present, then of event1, event2, ... up to the first missing number.
// A missing plain key does not stop the numbered ones. Each choice's Text is
// the value of the same key followed by "text" (select1text for select1).
// Values are cut to SoundTextMax characters; an empty value is kept as a
// silent choice.
func (c *SoundClass) Sounds(event string) []SoundChoice {
	var out []SoundChoice
	add := func(key string) bool {
		v, ok := values.Lookup(c.Events, key)
		if !ok {
			return false
		}
		text, _ := values.Lookup(c.Events, key+"text")
		out = append(out, SoundChoice{
			Sound: values.Truncate(v, SoundTextMax),
			Text:  values.Truncate(text, SoundTextMax),
		})
		return true
	}
	add(event)
	for n := 1; ; n++ {
		if !add(event + strconv.Itoa(n)) {
			break
		}
	}
	return out
}

// SoundEvent is one section of gamedata/allsound.tdf: a named UI/game event
// mapped to a single sound sample. Decode the whole file with
//
//	var events []ta.SoundEvent
//	err := tdf.Unmarshal(data, &events)
type SoundEvent struct {
	Key string `tdf:",name"` // section header, e.g. "BGM"

	Sound string `tdf:"sound,omitempty"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records which keys the source had, in order (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}
