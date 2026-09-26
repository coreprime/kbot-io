package tak

import (
	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// CanBuildGrant is one canbuild/<builder>/<unit>.tdf file — TA:Kingdoms'
// build-menu mechanism. The file's existence grants the pairing (its path
// names builder and unit); the body only carries menu placement.
type CanBuildGrant struct {
	Menu CanBuildMenu `tdf:"MENU"`

	// Sections preserves every other top-level section, in order.
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// CanBuildMenu is the [Menu] section: Priority orders the builder's menu
// (lower = earlier).
type CanBuildMenu struct {
	Priority int `tdf:"priority,omitempty"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}
