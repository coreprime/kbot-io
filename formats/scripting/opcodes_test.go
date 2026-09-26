package scripting

import "testing"

func TestOpcodeTableMeanings(t *testing.T) {
	tests := []struct {
		opcode           uint32
		name             string
		operands         int
		pops, pushes     int
		kingdoms, faults bool
	}{
		{0x10037000, "XOR", 0, 2, 1, false, false},
		{0x10038000, "NOT", 0, 1, 1, false, false},
		{0x10059000, "XOR_ALT", 0, 2, 1, false, false},
		{0x1003A000, "TAK_MATH_0A", 0, 2, 1, true, false},
		{0x10039000, "TAK_MATH_09", 0, 2, 1, true, false},
		{0x1003B000, "TAK_MATH_0B", 0, 2, 1, true, false},
		{0x10072000, "PLAY_SOUND", 1, 1, 1, true, false},
		{0x10073000, "MISSION_COMMAND", 2, VariablePops, 1, true, false},
		{0x10063000, "DISCARD_CALL", 2, VariablePops, 0, false, false},
		{0x10009000, "PIECE_OP_09", 1, 2, 0, false, false},
		{0x10044000, "IS_CARRYING_UNIT", 0, 1, 1, false, false},
		{0x10045000, "CARRIER_UNIT_ID", 0, 0, 1, false, false},
		{0x1000A000, "DONT_SHADOW", 1, 0, 0, false, false},
		{0x10043000, "GET", 0, 5, 1, false, false},
		{0x10021000, "PUSH_IMM", 1, 0, 1, false, true},
	}
	for _, tt := range tests {
		info, ok := LookupOpcode(tt.opcode)
		if !ok {
			t.Errorf("0x%08X: not in the table", tt.opcode)
			continue
		}
		if info.Name != tt.name || info.Operands != tt.operands || info.Pops != tt.pops ||
			info.Pushes != tt.pushes || info.Kingdoms != tt.kingdoms || info.Faults != tt.faults {
			t.Errorf("0x%08X = %+v, want %s operands=%d pops=%d pushes=%d kingdoms=%v faults=%v",
				tt.opcode, info, tt.name, tt.operands, tt.pops, tt.pushes, tt.kingdoms, tt.faults)
		}
		if got := OpcodeName(tt.opcode); got != tt.name {
			t.Errorf("OpcodeName(0x%08X) = %s, want %s", tt.opcode, got, tt.name)
		}
		if got := OpcodeParamCount(tt.opcode); got != tt.operands {
			t.Errorf("OpcodeParamCount(0x%08X) = %d, want %d", tt.opcode, got, tt.operands)
		}
	}
}

func TestDeprecatedOpcodeNamesKeepTheirValues(t *testing.T) {
	// Existing callers switch on these names; their values must not move.
	values := map[string][2]uint32{
		"OP_MOD":            {OP_MOD, 0x10037000},
		"OP_BITWISE_XOR":    {OP_BITWISE_XOR, 0x10038000},
		"OP_BITWISE_NOT":    {OP_BITWISE_NOT, 0x1003A000},
		"OP_LOGICAL_XOR":    {OP_LOGICAL_XOR, 0x10059000},
		"OP_PUSH_IMMEDIATE": {OP_PUSH_IMMEDIATE, 0x10021000},
		"OP_CREATE_LOCAL":   {OP_CREATE_LOCAL, 0x10021008},
	}
	for name, v := range values {
		if v[0] != v[1] {
			t.Errorf("%s = 0x%08X, want 0x%08X", name, v[0], v[1])
		}
	}
	if OP_XOR != OP_MOD || OP_NOT != OP_BITWISE_XOR || OP_TAK_MATH_0A != OP_BITWISE_NOT || OP_XOR_ALT != OP_LOGICAL_XOR {
		t.Error("new names must share the words of the names they replace")
	}
}

func TestDispatchOpcodeFollowsTheGame(t *testing.T) {
	tests := []struct {
		raw, want uint32
		name      string
		operands  int
	}{
		{0x10064001, OP_JUMP, "JUMP", 1}, // low bit ignored
		{0x12064000, OP_JUMP, "JUMP", 1}, // bits outside the mask ignored
		{0x10063FFF, OP_DISCARD_CALL, "DISCARD_CALL", 2},
		{0x10021009, OP_PUSH_CONSTANT, "PUSH_CONST", 1}, // flag = low three bits
		{0x10021008, 0x10021000, "PUSH_IMM", 1},         // flag 0: the game faults
		{0x10023006, 0x10023006, "UNKNOWN_0x10023006", 1},
		{0x10023002, OP_POP_LOCAL_VAR, "POP_LOCAL", 1},
		{0x10090000, 0x10090000, "UNKNOWN_0x10090000", 0},
		{0x00000005, 0x00000000, "UNKNOWN_0x00000000", 0},
	}
	for _, tt := range tests {
		if got := DispatchOpcode(tt.raw); got != tt.want {
			t.Errorf("DispatchOpcode(0x%08X) = 0x%08X, want 0x%08X", tt.raw, got, tt.want)
		}
		if got := OpcodeName(tt.raw); got != tt.name {
			t.Errorf("OpcodeName(0x%08X) = %s, want %s", tt.raw, got, tt.name)
		}
		if got := OpcodeParamCount(tt.raw); got != tt.operands {
			t.Errorf("OpcodeParamCount(0x%08X) = %d, want %d", tt.raw, got, tt.operands)
		}
	}
}

func TestOpcodeByNameReadsEveryForm(t *testing.T) {
	for op, info := range opcodeTable {
		got, ok := OpcodeByName(info.Name)
		if !ok || got != op {
			t.Errorf("OpcodeByName(%q) = 0x%08X, %v; want 0x%08X", info.Name, got, ok, op)
		}
	}
	tests := []struct {
		name string
		want uint32
		ok   bool
	}{
		// Mnemonics of earlier listings assemble to the words they meant.
		{"MOD", 0x10037000, true},
		{"BITWISE_XOR", 0x10038000, true},
		{"BITWISE_NOT", 0x1003A000, true},
		{"LOGICAL_XOR", 0x10059000, true},
		{"CREATE_LOCAL", 0x10021008, true},
		{"PUSH_IMMEDIATE", 0x10021000, true},
		{"GREATER_OR_EQUAL", OP_GREATER_EQUAL, true},
		// Raw forms.
		{"JUMP@0x10064001", 0x10064001, true},
		{"PUSH_IMM@0x10021008", 0x10021008, true},
		{"UNKNOWN_0x10090000", 0x10090000, true},
		{"UNKNOWN_0x10090000@0x10090003", 0x10090003, true},
		{"0x10065000", 0x10065000, true},
		{"JUMP@0x10065000", 0, false}, // the word is RETURN, not JUMP
		{"JUMP@", 0, false},
		{"NOPE", 0, false},
		{"UNKNOWN_zz", 0, false},
	}
	for _, tt := range tests {
		got, ok := OpcodeByName(tt.name)
		if ok != tt.ok || got != tt.want {
			t.Errorf("OpcodeByName(%q) = 0x%08X, %v; want 0x%08X, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

func TestInstructionMnemonicRoundTrips(t *testing.T) {
	for _, raw := range []uint32{OP_JUMP, 0x10064001, 0x10021008, 0x10090000, 0x10090007, OP_XOR, 0} {
		inst := Instruction{Opcode: DispatchOpcode(raw), Raw: raw}
		got, ok := OpcodeByName(inst.Mnemonic())
		if !ok || got != raw {
			t.Errorf("Mnemonic of 0x%08X = %q reads back as 0x%08X, %v", raw, inst.Mnemonic(), got, ok)
		}
	}
}

func TestInstructionStackEffect(t *testing.T) {
	call := Instruction{Opcode: OP_CALL_SCRIPT, Operand: 3, Operand2: 2}
	if pops, pushes, ok := call.StackEffect(); !ok || pops != 2 || pushes != 0 {
		t.Errorf("CALL_SCRIPT with 2 args: pops=%d pushes=%d ok=%v", pops, pushes, ok)
	}
	if _, _, ok := (Instruction{Opcode: 0x10090000}).StackEffect(); ok {
		t.Error("an unknown word has no stack effect")
	}
}

func TestOpcodesListsTheTableInOrder(t *testing.T) {
	list := Opcodes()
	if len(list) != len(opcodeTable) {
		t.Fatalf("%d entries, table has %d", len(list), len(opcodeTable))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Opcode >= list[i].Opcode {
			t.Fatalf("not ordered at %d: 0x%08X, 0x%08X", i, list[i-1].Opcode, list[i].Opcode)
		}
	}
}
