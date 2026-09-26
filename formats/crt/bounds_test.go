package crt

import (
	"encoding/binary"
	"strings"
	"testing"
)

// words encodes little-endian uint32 values.
func words(v ...uint32) []byte {
	out := make([]byte, 4*len(v))
	for i, w := range v {
		binary.LittleEndian.PutUint32(out[4*i:], w)
	}
	return out
}

// TestLoadRejectsImpossibleCounts feeds tiny files whose counts would drive
// preallocations of gigabytes; each must fail with an error naming the count.
func TestLoadRejectsImpossibleCounts(t *testing.T) {
	head := words(Signature, 0, 0) // signature, header word, no units
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"units", words(Signature, 0, 0xFFFFFFFF), "unit count"},
		{"players", append(head, words(0xFFFFFFFF)...), "player count"},
		{"rules", append(head, words(1, 0xFFFFFFFF)...), "rule count"},
		{"conditions", append(head, words(1, 1, 0xFFFFFFFF, 0)...), "clause count"},
		{"actions", append(head, words(1, 1, 0, 0xFFFFFFFF)...), "clause count"},
		{"triggers", append(head, words(0, 0xFFFFFFFF)...), "trigger count"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(c.data)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming the %s", err, c.want)
			}
		})
	}
}

func TestLoadMinimalScript(t *testing.T) {
	// No units, one player with one rule holding one condition and no
	// actions, no triggers.
	data := append(words(Signature, 0, 0, 1, 1, 1, 7), make([]byte, argsPerClause*argSize)...)
	data = append(data, words(0, 0)...)
	f, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Players) != 1 || f.RuleCount() != 1 || f.Players[0].Rules[0].Conditions[0].Opcode != 7 {
		t.Fatalf("decoded %+v", f)
	}
}
