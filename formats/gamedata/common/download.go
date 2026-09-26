package common

import (
	"fmt"
	"strings"

	"github.com/coreprime/kbot-io/formats/gamedata/internal/values"
	"github.com/coreprime/kbot-io/formats/tdf"
)

// DownloadFile is a download/*.tdf add-on menu document — the mechanism
// downloadable units in both games use to graft themselves onto an existing
// builder's construction menu (AFark.ufo ships the canonical example).
//
// TA 3.1c reads each download/*.tdf file this way:
//
//   - It takes the first DownloadMenuLimit (5) sections of the file, whatever
//     their names; retail files call them [MENUENTRY1], [MENUENTRY2], ... but
//     [ENTRY0] works as well. Later sections are ignored. Menus returns the
//     sections it reads.
//   - An entry whose UNITMENU is missing, or names no loaded unit, is ignored.
//     UNITMENU and UNITNAME are compared ignoring case and hold at most
//     DownloadNameMax (31) characters.
//   - MENU and BUTTON are kept to 8 bits (0..255).
//   - A builder's whole build list, its CANBUILD entries plus the units the
//     download files add, holds at most 31 units.
//
// Check reports entries the game would ignore or read differently.
type DownloadFile struct {
	// Entries holds every section of the file in order, whatever its name
	// (the game reads sections by position, not by name). An entry with an
	// empty Key is written as [MENUENTRY<n>], n the lowest number from 0 no
	// other entry is named with.
	Entries []MenuEntry `tdf:"MENUENTRY,sections"`

	// Remaining preserves any key=value outside the sections.
	Remaining map[string]string `tdf:",remaining"`

	// Meta records the source's section order and spelling (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

// MenuEntry is one [MENUENTRYn] section: UnitMenu names the builder whose
// menu gains the unit, UnitName the unit being added, and Menu/Button the
// page and slot it lands on.
type MenuEntry struct {
	// Key is the section's name, such as MENUENTRY1. The game ignores it; a
	// new entry with an empty Key is written as [MENUENTRY<n>] (see
	// DownloadFile.Entries), and AddEntry names it as retail files do.
	Key string `tdf:",name"`

	UnitMenu string `tdf:"unitmenu,omitempty"`
	Menu     int    `tdf:"menu,omitempty"`
	Button   int    `tdf:"button,omitempty"`
	UnitName string `tdf:"unitname,omitempty"`

	// Remaining preserves every other key=value so the file round-trips.
	Remaining map[string]string `tdf:",remaining"`

	// Sections preserves any section nested in the entry.
	Sections []Section `tdf:",sections"`

	// Meta records which keys the source had, in what order and with what
	// text, so an explicit zero survives a round trip (see tdf.Meta).
	Meta tdf.Meta `tdf:",meta"`
}

const (
	// DownloadMenuLimit is how many sections of a download file the game
	// reads.
	DownloadMenuLimit = 5
	// DownloadNameMax is the longest UNITMENU or UNITNAME the game keeps.
	DownloadNameMax = 31
	// BuildListLimit is the most units a builder's build list holds, its
	// CANBUILD entries and download additions together.
	BuildListLimit = 31
)

// Menus returns the entries the game reads: the first DownloadMenuLimit
// sections of the file, whatever their names.
func (d *DownloadFile) Menus() []MenuEntry {
	if len(d.Entries) > DownloadMenuLimit {
		return d.Entries[:DownloadMenuLimit]
	}
	return d.Entries
}

// AddEntry appends e, naming it MENUENTRY<n> when its Key is empty, with n
// counting from 1 as in retail files (the number of entries after adding it,
// or the next number no other entry is named with), and returns a pointer to
// the stored entry.
func (d *DownloadFile) AddEntry(e MenuEntry) *MenuEntry {
	if e.Key == "" {
		used := map[string]bool{}
		for _, other := range d.Entries {
			used[strings.ToUpper(other.Key)] = true
		}
		n := len(d.Entries) + 1
		for used[fmt.Sprintf("MENUENTRY%d", n)] {
			n++
		}
		e.Key = fmt.Sprintf("MENUENTRY%d", n)
	}
	d.Entries = append(d.Entries, e)
	return &d.Entries[len(d.Entries)-1]
}

// Check reports what the game ignores or reads differently in the file:
// sections past the fifth, entries with no UNITMENU, MENU or BUTTON values
// outside 0..255 and names longer than the game keeps.
func (d *DownloadFile) Check() []Warning {
	var out []Warning
	for i, e := range d.Entries {
		section := e.Key
		if i >= DownloadMenuLimit {
			out = append(out, Warning{Section: section, Message: fmt.Sprintf(
				"section %d of the file; the game reads only the first %d", i+1, DownloadMenuLimit)})
			continue
		}
		if e.Key == "" {
			out = append(out, Warning{Message: fmt.Sprintf("section %d has no name", i+1)})
		}
		if e.UnitMenu == "" {
			out = append(out, Warning{Section: section, Key: "UNITMENU",
				Message: "missing or empty; the game ignores the entry"})
		}
		for _, f := range []struct {
			key string
			v   int
		}{{"MENU", e.Menu}, {"BUTTON", e.Button}} {
			if f.v < 0 || f.v > 255 {
				out = append(out, Warning{Section: section, Key: f.key, Message: fmt.Sprintf(
					"%d is outside 0..255; the game keeps the low 8 bits (%d)", f.v, uint8(f.v))})
			}
		}
		for _, f := range []struct{ key, v string }{{"UNITMENU", e.UnitMenu}, {"UNITNAME", e.UnitName}} {
			if len(f.v) > DownloadNameMax {
				out = append(out, Warning{Section: section, Key: f.key, Message: fmt.Sprintf(
					"%d characters; the game keeps the first %d (%q)", len(f.v), DownloadNameMax,
					values.Truncate(f.v, DownloadNameMax))})
			}
		}
	}
	return out
}
