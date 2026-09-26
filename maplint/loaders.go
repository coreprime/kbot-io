package maplint

import (
	"errors"
	"strconv"

	"github.com/coreprime/kbot-io/formats/gamedata/ta"
)

// ParseOTA reads a TA .ota file's text the way the game does and extracts the
// fields the lint cares about. It returns nil when the file has no
// [GlobalHeader] (probably not an OTA at all).
//
// Schemas holds the schemas the game can use: Schema 0, Schema 1, ... (names
// compared ignoring case) up to the first missing number, in number order,
// each Name being its number. Schema sections the game never reads are listed
// in UnreachableSchemas. Each schema's StartPos holds every [specials] entry
// whose specialwhat starts with StartPos, numbered as the game numbers them:
// StartPos0 and entries with no number are kept (see ta.StartPosition).
func ParseOTA(content string) (*OTAInfo, error) {
	m, err := ta.ReadMap([]byte(content))
	if errors.Is(err, ta.ErrNoGlobalHeader) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h := &m.Header
	out := &OTAInfo{
		MissionName:        h.MissionName,
		MissionDescription: h.MissionDescription,
		Planet:             h.Planet,
		NumPlayers:         h.NumPlayersText(),
		Size:               h.Size,
		SeaLevel:           h.SeaLevel,
		UnreachableSchemas: h.UnreachableSchemas(),
	}
	for n, s := range h.GameSchemas() {
		schema := SchemaInfo{
			Name:         strconv.Itoa(n),
			Type:         s.Type,
			SurfaceMetal: s.SurfaceMetal,
		}
		for _, p := range s.StartPositions() {
			schema.StartPos = append(schema.StartPos, StartPos{Number: p.Number, Slot: p.Slot, X: p.X, Z: p.Z})
		}
		out.Schemas = append(out.Schemas, schema)
	}
	return out, nil
}
