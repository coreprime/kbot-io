package assembly

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/scripting"
)

func TestAssemblerDirectives(t *testing.T) {
	input := `.version 4
.statics 4
.piece base
.piece turret

.script Create
0000  PUSH_CONST           0
0008  RETURN
`
	asm := NewAssembler()
	cob, err := asm.Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cob.VersionSignature != 4 {
		t.Errorf("Version = %d, want 4", cob.VersionSignature)
	}
	if cob.NumberOfStaticVars != 4 {
		t.Errorf("NumberOfStaticVars = %d, want 4", cob.NumberOfStaticVars)
	}
	if len(cob.PieceNames) != 2 {
		t.Errorf("PieceNames = %v, want [base turret]", cob.PieceNames)
	}
	if cob.NumScripts != 1 {
		t.Errorf("NumScripts = %d, want 1", cob.NumScripts)
	}
	if len(cob.ScriptNames) != 1 || cob.ScriptNames[0] != "Create" {
		t.Errorf("ScriptNames = %v, want [Create]", cob.ScriptNames)
	}
}

func TestAssemblerNoScripts(t *testing.T) {
	_, err := NewAssembler().Assemble(".version 4\n")
	if err == nil {
		t.Fatal("expected error for listing with no scripts")
	}
}

func TestAssemblerKeepsRawWords(t *testing.T) {
	listing := `.version 4
.field5 3
.script Create
0000  JUMP@0x10064001      3
0008  DISCARD_CALL         5, 2
0014  UNKNOWN_0x10090000
0018  RETURN
`
	cob, err := NewAssembler().Assemble(listing)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want := []uint32{0x10064001, 3, scripting.OP_DISCARD_CALL, 5, 2, 0x10090000, scripting.OP_RETURN}
	if len(cob.Code) != 4*len(want) {
		t.Fatalf("code is %d bytes, want %d", len(cob.Code), 4*len(want))
	}
	for i, w := range want {
		if got := binary.LittleEndian.Uint32(cob.Code[i*4:]); got != w {
			t.Errorf("word %d = 0x%08X, want 0x%08X", i, got, w)
		}
	}
	if cob.UKZero != 3 {
		t.Errorf("UKZero = %d, want 3", cob.UKZero)
	}

	// Disassembling prints the same forms back.
	insts, err := cob.Disassemble(0)
	if err != nil {
		t.Fatal(err)
	}
	text := NewDisassembler(insts, "Create", 0).Render(Plain)
	for _, form := range []string{"JUMP@0x10064001", "DISCARD_CALL         5, 2", "UNKNOWN_0x10090000"} {
		if !strings.Contains(text, form) {
			t.Errorf("listing lacks %q:\n%s", form, text)
		}
	}
	again, err := NewAssembler().Assemble(".version 4\n.field5 3\n" + text)
	if err != nil {
		t.Fatalf("reassemble: %v", err)
	}
	if !bytes.Equal(again.Code, cob.Code) {
		t.Error("disassemble/assemble changed the code")
	}
}

func TestAssemblerReadsEarlierMnemonics(t *testing.T) {
	// Listings written before the opcode names were corrected assemble to
	// the words they were made from.
	listing := `.version 4
.script Create
0000  MOD
0004  BITWISE_XOR
0008  BITWISE_NOT
000C  LOGICAL_XOR
0010  RETURN
`
	cob, err := NewAssembler().Assemble(listing)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	want := []uint32{0x10037000, 0x10038000, 0x1003A000, 0x10059000, scripting.OP_RETURN}
	for i, w := range want {
		if got := binary.LittleEndian.Uint32(cob.Code[i*4:]); got != w {
			t.Errorf("word %d = 0x%08X, want 0x%08X", i, got, w)
		}
	}
}
