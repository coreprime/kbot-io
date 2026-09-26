package common

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

func TestParsePlayerCounts(t *testing.T) {
	for in, want := range map[string]string{
		"2, 3, 4":     "[2 3 4]",
		"2 3 4":       "[2 3 4]",
		"2-8":         "[2 3 4 5 6 7 8]",
		"2 to 4, 10":  "[2 3 4 10]",
		"Any":         "[]",
		"":            "[]",
		"4, 2, 4":     "[4 2]",
		"0, 3":        "[3]",
		"tomato 2-3":  "[2 3]",
		"1-99":        "[1 2 3 4 5 6 7 8 9 10 99]",
		"2 players 4": "[2 4]",
	} {
		got := ParsePlayerCounts(in)
		if s := fmt.Sprint(got); s != want {
			t.Errorf("ParsePlayerCounts(%q) = %v, want %s", in, got, want)
		}
	}
}

func TestNumPlayersKeepsItsText(t *testing.T) {
	var h struct {
		GlobalHeaderBase
	}
	src := "numplayers=2 3 4;"
	if err := tdf.Unmarshal([]byte(src), &h); err != nil {
		t.Fatalf("a numplayers value that is not a comma list failed the file: %v", err)
	}
	if h.NumPlayersText() != "2 3 4" || fmt.Sprint(h.PlayerCounts()) != "[2 3 4]" {
		t.Errorf("text %q counts %v", h.NumPlayersText(), h.PlayerCounts())
	}
	h.NumPlayers = []int{2, 4}
	if h.NumPlayersText() != "2, 4" {
		t.Errorf("changed list: %q", h.NumPlayersText())
	}
	h.SetNumPlayersText("Any")
	out, err := tdf.Marshal(&h)
	if err != nil || !strings.Contains(string(out), "numplayers=Any;") {
		t.Errorf("SetNumPlayersText: %s %v", out, err)
	}
}
