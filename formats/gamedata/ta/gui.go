package ta

import (
	"github.com/coreprime/kbot-io/formats/gamedata/common"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// GUICommon is the [COMMON] subsection shared by every GUI gadget.
type GUICommon struct {
	ID            int    `tdf:"id"`
	Assoc         int    `tdf:"assoc,omitempty"`
	Name          string `tdf:"name,omitempty"`
	XPos          int    `tdf:"xpos,omitempty"`
	YPos          int    `tdf:"ypos,omitempty"`
	Width         int    `tdf:"width,omitempty"`
	Height        int    `tdf:"height,omitempty"`
	Attribs       int    `tdf:"attribs,omitempty"`
	ColorF        int    `tdf:"colorf,omitempty"`
	ColorB        int    `tdf:"colorb,omitempty"`
	TextureNumber int    `tdf:"texturenumber,omitempty"`
	FontNumber    int    `tdf:"fontnumber,omitempty"`
	Active        int    `tdf:"active,omitempty"`
	CommonAttribs int    `tdf:"commonattribs,omitempty"`

	// Help is the gadget's help text and GafFile its image-file number; the
	// game reads both from [COMMON].
	Help    string `tdf:"help,omitempty"`
	GafFile int    `tdf:"gaffile,omitempty"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves any section nested in [COMMON].
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// GUIVersion is the [VERSION] subsection carried by the first gadget of a panel.
type GUIVersion struct {
	Major    int `tdf:"major,omitempty"`
	Minor    int `tdf:"minor,omitempty"`
	Revision int `tdf:"revision,omitempty"`

	// Remaining preserves any other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records which keys the source had, so an all-zero [VERSION] is
	// written back rather than dropped (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// Gadget is one [GADGETn] entry of a GUI panel (.gui) file. Decode a file with
//
//	var gadgets []ta.Gadget
//	err := tdf.Unmarshal(data, &gadgets)
type Gadget struct {
	Key string `tdf:",name"` // section header, e.g. GADGET0

	Common  GUICommon   `tdf:"common"`
	Version *GUIVersion `tdf:"version,omitempty"`

	TotalGadgets int    `tdf:"totalgadgets,omitempty"`
	Panel        string `tdf:"panel,omitempty"`
	Filename     string `tdf:"filename,omitempty"`

	Text      string `tdf:"text,omitempty"`
	QuickKey  string `tdf:"quickkey,omitempty"` // key char or virtual-key code, e.g. "O" or "-68"
	Status    int    `tdf:"status,omitempty"`
	GrayedOut int    `tdf:"grayedout,omitempty"`
	// Help holds a help key written beside [COMMON] rather than in it. The
	// game reads help text only from [COMMON] (GUICommon.Help), so retail
	// layouts leave this empty.
	Help       string `tdf:"help,omitempty"`
	Stages     int    `tdf:"stages,omitempty"`
	HotOrNot   int    `tdf:"hotornot,omitempty"`
	MaxChars   int    `tdf:"maxchars,omitempty"`
	ItemHeight int    `tdf:"itemheight,omitempty"`

	// Focus/default targets reference other gadgets by name (e.g. "OK").
	Link         string `tdf:"link,omitempty"`
	EscDefault   string `tdf:"escdefault,omitempty"`
	CrDefault    string `tdf:"crdefault,omitempty"`
	CrtDefault   string `tdf:"crtdefault,omitempty"`
	DefaultFocus string `tdf:"defaultfocus,omitempty"`

	// Slider-style gadgets.
	Thick    int `tdf:"thick,omitempty"`
	Range    int `tdf:"range,omitempty"`
	KnobSize int `tdf:"knobsize,omitempty"`
	KnobPos  int `tdf:"knobpos,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves every other nested section, including a second
	// [COMMON] (the game reads the first), in order.
	Sections []common.Section `tdf:",sections"`

	// Meta records which keys and sections the source had, in what order
	// and with what text (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}
