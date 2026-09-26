package scripting

import (
	"encoding/binary"
	"errors"
	"testing"
)

func words(ws ...uint32) []byte {
	out := make([]byte, 4*len(ws))
	for i, w := range ws {
		binary.LittleEndian.PutUint32(out[i*4:], w)
	}
	return out
}

func TestDisassembleEndsAtNearestLaterEntry(t *testing.T) {
	// Entries at words 0, 10 and 4: script 0 must stop at word 4, not 10.
	code := words(
		OP_PUSH_CONSTANT, 1, OP_RETURN, OP_RETURN, // script 0: words 0-3
		OP_PUSH_CONSTANT, 2, OP_RETURN, OP_RETURN, OP_RETURN, OP_RETURN, // script 2: words 4-9
		OP_RETURN, // script 1: word 10
	)
	cob := &COB{NumScripts: 3, ScriptCodeIndices: []uint32{0, 10, 4}, Code: code}
	insts, err := cob.Disassemble(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 3 {
		t.Errorf("script 0 has %d instructions, want 3: %v", len(insts), insts)
	}
}

func TestDisassembleRejectsHugeEntryIndex(t *testing.T) {
	// 0x40000001 * 4 wraps to 4 in 32 bits.
	cob := &COB{NumScripts: 1, ScriptCodeIndices: []uint32{0x40000001}, Code: words(OP_RETURN, OP_RETURN)}
	if _, err := cob.Disassemble(0); err == nil {
		t.Fatal("expected an error for an entry past the code")
	}
}

func TestDisassembleReportsTruncatedOperands(t *testing.T) {
	cob := &COB{NumScripts: 1, ScriptCodeIndices: []uint32{0}, Code: words(OP_PUSH_CONSTANT, 5, OP_JUMP)}
	insts, err := cob.Disassemble(0)
	if !errors.Is(err, ErrTruncatedInstruction) {
		t.Fatalf("err = %v, want ErrTruncatedInstruction", err)
	}
	if len(insts) != 1 || insts[0].Operand != 5 {
		t.Errorf("instructions before the damage = %v", insts)
	}
}

func TestDisassembleUsesTheGameDispatch(t *testing.T) {
	code := words(
		0x10064001, 3, // JUMP with its low bit set: the game still jumps
		OP_DISCARD_CALL, 5, 2, // two inline words
		OP_PIECE_OP_09, 1, // one inline word
		OP_IS_CARRYING_UNIT,
		OP_RETURN,
	)
	cob := &COB{NumScripts: 1, ScriptCodeIndices: []uint32{0}, Code: code}
	insts, err := cob.Disassemble(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		op        uint32
		raw       uint32
		op1, op2  int32
		mnemonics string
	}{
		{OP_JUMP, 0x10064001, 3, 0, "JUMP@0x10064001"},
		{OP_DISCARD_CALL, OP_DISCARD_CALL, 5, 2, "DISCARD_CALL"},
		{OP_PIECE_OP_09, OP_PIECE_OP_09, 1, 0, "PIECE_OP_09"},
		{OP_IS_CARRYING_UNIT, OP_IS_CARRYING_UNIT, 0, 0, "IS_CARRYING_UNIT"},
		{OP_RETURN, OP_RETURN, 0, 0, "RETURN"},
	}
	if len(insts) != len(want) {
		t.Fatalf("got %d instructions: %v", len(insts), insts)
	}
	for i, w := range want {
		in := insts[i]
		if in.Opcode != w.op || in.Raw != w.raw || in.Operand != w.op1 || in.Operand2 != w.op2 || in.Mnemonic() != w.mnemonics {
			t.Errorf("[%d] = %+v (%s), want %+v", i, in, in.Mnemonic(), w)
		}
	}
}
