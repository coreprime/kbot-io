package scripting

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Total Annihilation COB opcode definitions.
//
// Every instruction is a 32-bit little-endian opcode word followed by zero,
// one or two inline operand words. The game does not compare the whole word:
// it dispatches on raw & OpcodeDispatchMask, so bits outside the mask (for
// example the low bit of 0x10064001) are ignored and such a word runs as the
// base instruction. PUSH (0x10021000) and POP (0x10023000) then read their
// source or destination from the low three bits: PUSH accepts 1 (constant),
// 2 (local) and 4 (static); POP accepts 2 (local) and 4 (static). Any other
// flag value makes the game fault. DispatchOpcode reduces a raw word to the
// canonical encoding used by the constants below.

// OpcodeDispatchMask selects the bits of an opcode word that the game
// dispatches on.
const OpcodeDispatchMask = 0x100ff000

const (
	opPushBase = 0x10021000 // PUSH; the low three bits select the source
	opPopBase  = 0x10023000 // POP; the low three bits select the destination
	opFlagMask = 0x7
)

const (
	// Animation commands - object manipulation
	OP_MOVE       = 0x10001000 // Move object (speed & distance from stack, then piece#, axis#)
	OP_TURN       = 0x10002000 // Turn object (speed & direction from stack, then piece#, axis#)
	OP_SPIN       = 0x10003000 // Spin object (speed from stack, then piece#, axis#)
	OP_STOP_SPIN  = 0x10004000 // Stop spin (piece#, axis# after)
	OP_SHOW       = 0x10005000 // Show object (piece# after)
	OP_HIDE       = 0x10006000 // Hide object (piece# after)
	OP_CACHE      = 0x10007000 // Cache object (piece# after)
	OP_DONT_CACHE = 0x10008000 // Don't cache (piece# after)
	OP_TURN_NOW   = 0x1000C000 // Turn immediately (piece#, axis#, angle after)
	OP_MOVE_NOW   = 0x1000B000 // Move immediately (piece#, axis#, position after)
	OP_SHADE      = 0x1000D000 // Shade object (piece# after)
	OP_DONT_SHADE = 0x1000E000 // Don't shade (piece# after)
	OP_EMIT_SFX   = 0x1000F000 // Emit SFX (type from stack, then piece#)

	// Wait operations
	OP_WAIT_FOR_TURN = 0x10011000 // Wait for turn (piece#, axis# after)
	OP_WAIT_FOR_MOVE = 0x10012000 // Wait for move (piece#, axis# after)
	OP_SLEEP         = 0x10013000 // Sleep (time from stack)

	// Stack operations
	OP_PUSH_CONSTANT  = 0x10021001 // Push constant <value>
	OP_PUSH_LOCAL_VAR = 0x10021002 // Push local var <var#>
	OP_PUSH_STATIC    = 0x10021004 // Push static var <var#>

	// OP_CREATE_LOCAL is a PUSH whose low three bits are 0.
	//
	// Deprecated: the game faults on a PUSH without source flag 1, 2 or 4,
	// and dispatches this word as the flagless PUSH (OP_PUSH_IMMEDIATE).
	// Reserve a local with OP_STACK_ALLOC.
	OP_CREATE_LOCAL = 0x10021008

	// OP_PUSH_IMMEDIATE is the PUSH encoding with no source flag.
	//
	// Deprecated: the game faults on a PUSH without source flag 1, 2 or 4.
	// Push a literal with OP_PUSH_CONSTANT.
	OP_PUSH_IMMEDIATE = 0x10021000

	OP_STACK_ALLOC   = 0x10022000 // Allocate local variable (no param)
	OP_POP_LOCAL_VAR = 0x10023002 // Pop to local var <var#>
	OP_POP_STATIC    = 0x10023004 // Pop to static var <var#>
	OP_POP_STACK     = 0x10024000 // Pop and discard top of stack

	// Arithmetic operations
	OP_ADD = 0x10031000 // Add (both from stack)
	OP_SUB = 0x10032000 // Subtract (both from stack)
	OP_MUL = 0x10033000 // Multiply (both from stack)
	OP_DIV = 0x10034000 // Divide (both from stack)

	// Bitwise operations
	OP_BITWISE_AND = 0x10035000 // Bitwise AND
	OP_BITWISE_OR  = 0x10036000 // Bitwise OR
	OP_XOR         = 0x10037000 // Bitwise XOR (both from stack)
	OP_NOT         = 0x10038000 // Bitwise NOT (one value from stack)

	// OP_MOD carries the value some published opcode tables give modulo.
	//
	// Deprecated: the game has no modulo instruction. It executes 0x10037000
	// as bitwise XOR; use OP_XOR.
	OP_MOD = 0x10037000

	// OP_BITWISE_XOR carries the value some published opcode tables give XOR.
	//
	// Deprecated: the game executes 0x10038000 as the unary bitwise NOT
	// (OP_NOT). Bitwise XOR is OP_XOR (0x10037000).
	OP_BITWISE_XOR = 0x10038000

	// OP_BITWISE_NOT carries the value some published opcode tables give NOT.
	//
	// Deprecated: TA does not execute 0x1003A000; it is the TA: Kingdoms
	// operator OP_TAK_MATH_0A. The bitwise NOT is OP_NOT (0x10038000).
	OP_BITWISE_NOT = 0x1003A000

	// Special functions
	OP_RAND           = 0x10041000 // Random (low & high from stack)
	OP_GET_UNIT_VALUE = 0x10042000 // Get unit value (port# from stack)
	OP_GET            = 0x10043000 // Get value (port and four arguments from stack)

	// OP_IS_CARRYING_UNIT pops a unit id and pushes 1 when that unit rides
	// on this one, else 0.
	OP_IS_CARRYING_UNIT = 0x10044000

	// OP_CARRIER_UNIT_ID pushes the id of the unit carrying this one, or 0.
	OP_CARRIER_UNIT_ID = 0x10045000

	// Comparison operations
	OP_LESS_THAN     = 0x10051000 // <  (both from stack)
	OP_LESS_OR_EQUAL = 0x10052000 // <= (both from stack)
	OP_GREATER_THAN  = 0x10053000 // >  (both from stack)
	OP_GREATER_EQUAL = 0x10054000 // >= (both from stack)
	OP_EQUAL         = 0x10055000 // == (both from stack)
	OP_NOT_EQUAL     = 0x10056000 // != (both from stack)

	// Logical operations
	OP_LOGICAL_AND = 0x10057000 // && (both from stack)
	OP_LOGICAL_OR  = 0x10058000 // || (both from stack)

	// OP_XOR_ALT is the XOR in the logical-operator group. The game computes
	// the bitwise XOR of its two operands (6 and 3 give 5), exactly like
	// OP_XOR; it is not a logical XOR.
	OP_XOR_ALT = 0x10059000

	// OP_LOGICAL_XOR is the old name of OP_XOR_ALT.
	//
	// Deprecated: the game computes the bitwise XOR of the operands, not a
	// logical XOR. Use OP_XOR_ALT.
	OP_LOGICAL_XOR = 0x10059000

	OP_LOGICAL_NOT = 0x1005A000 // !  (from stack)

	// Control flow
	OP_START_SCRIPT    = 0x10061000 // Start script (params from stack, then script#, param_count)
	OP_CALL_SCRIPT     = 0x10062000 // Call script (params from stack, then script#, param_count)
	OP_JUMP            = 0x10064000 // Jump <absolute code word>
	OP_RETURN          = 0x10065000 // Return (value from stack)
	OP_JUMP_IF_FALSE   = 0x10066000 // Jump if false (test from stack, <absolute code word>)
	OP_SIGNAL          = 0x10067000 // Signal (signal# from stack)
	OP_SET_SIGNAL_MASK = 0x10068000 // Set signal mask (mask from stack)

	// OP_DISCARD_CALL takes two inline words; the second is an argument
	// count. The game pops that many values (it copies them into a
	// four-word buffer, so more than four overruns it) and continues with
	// the next instruction without running anything.
	OP_DISCARD_CALL = 0x10063000

	// Special effects
	OP_EXPLODE = 0x10071000 // Explode (type from stack, then piece#)

	// OP_PLAY_SOUND is a TA: Kingdoms instruction (sound# from stack,
	// volume inline, pushes a result). TA 3.1c has no handler for it and
	// the script faults when it reaches one.
	OP_PLAY_SOUND = 0x10072000

	// Set operations
	OP_SET_VALUE   = 0x10082000 // Set unit value (port# from stack, value from stack)
	OP_ATTACH_UNIT = 0x10083000 // Attach unit (unit, piece and flag from stack)
	OP_DROP_UNIT   = 0x10084000 // Drop unit (unit from stack)

	// OP_PIECE_OP_09 takes one inline piece number and pops two values. TA
	// executes it but its unit scripts do nothing with it; no retail script
	// uses it.
	OP_PIECE_OP_09 = 0x10009000

	// DONT_SHADOW disables shadow casting for a single piece. It shares the
	// shape of the other animation opcodes (one inline piece# DWORD) and
	// only appears in retail TA: Kingdoms .cob files (Scriptor's keyword
	// `dont-shadow` lives in the same animation category as `dont-shade`).
	// TA dispatches it and does nothing.
	OP_DONT_SHADOW = 0x1000A000

	// MISSION_COMMAND invokes a named TAK engine command. Two inline DWORDs
	// encode (soundNameIndex, stackArgCount); the engine pops `stackArgCount`
	// values off the stack, executes the command stored at index
	// `soundNameIndex` of the per-COB sound-name table, and pushes a result
	// back. The result is consumed by whichever POP_* opcode follows
	// (POP_STATIC/POP_LOCAL for assignment, POP_STACK to discard). Maps to
	// Scriptor's `Mission-Command(STRING, args...)` keyword. TA 3.1c has no
	// handler for it.
	OP_MISSION_COMMAND = 0x10073000

	// OP_TAK_MATH_09, OP_TAK_MATH_0A and OP_TAK_MATH_0B are TA: Kingdoms
	// arithmetic operators whose exact meaning is not documented. Retail
	// TA: Kingdoms scripts use each of them as a binary operator (two
	// values popped, one pushed) when packing and unpacking bit fields. TA
	// 3.1c has no handler for any of them.
	OP_TAK_MATH_09 = 0x10039000
	OP_TAK_MATH_0A = 0x1003A000
	OP_TAK_MATH_0B = 0x1003B000
)

// VariablePops is the OpcodeInfo.Pops value of instructions whose pop count
// is their second inline operand (START_SCRIPT, CALL_SCRIPT, DISCARD_CALL
// and MISSION_COMMAND).
const VariablePops = -1

// OpcodeInfo describes one instruction encoding.
type OpcodeInfo struct {
	// Opcode is the canonical encoding (see DispatchOpcode).
	Opcode uint32
	// Name is the mnemonic the disassembler prints and the assembler reads.
	Name string
	// Operands is the number of inline operand words after the opcode word.
	Operands int
	// Pops is the number of stack values the instruction consumes, or
	// VariablePops. RETURN is listed as popping nothing: the game pops its
	// value only when the engine asked for a return value.
	Pops int
	// Pushes is the number of values the instruction leaves on the stack.
	Pushes int
	// Kingdoms marks TA: Kingdoms extensions. TA 3.1c has no handler for
	// them and the script faults when it reaches one.
	Kingdoms bool
	// Faults marks encodings the game dispatches but refuses (a PUSH with
	// no source flag).
	Faults bool
}

var opcodeTable = func() map[uint32]OpcodeInfo {
	infos := []OpcodeInfo{
		// Animation
		{Opcode: OP_MOVE, Name: "MOVE", Operands: 2, Pops: 2},
		{Opcode: OP_TURN, Name: "TURN", Operands: 2, Pops: 2},
		{Opcode: OP_SPIN, Name: "SPIN", Operands: 2, Pops: 2},
		{Opcode: OP_STOP_SPIN, Name: "STOP_SPIN", Operands: 2, Pops: 1},
		{Opcode: OP_SHOW, Name: "SHOW", Operands: 1},
		{Opcode: OP_HIDE, Name: "HIDE", Operands: 1},
		{Opcode: OP_CACHE, Name: "CACHE", Operands: 1},
		{Opcode: OP_DONT_CACHE, Name: "DONT_CACHE", Operands: 1},
		{Opcode: OP_PIECE_OP_09, Name: "PIECE_OP_09", Operands: 1, Pops: 2},
		{Opcode: OP_DONT_SHADOW, Name: "DONT_SHADOW", Operands: 1},
		{Opcode: OP_MOVE_NOW, Name: "MOVE_NOW", Operands: 2, Pops: 1},
		{Opcode: OP_TURN_NOW, Name: "TURN_NOW", Operands: 2, Pops: 1},
		{Opcode: OP_SHADE, Name: "SHADE", Operands: 1},
		{Opcode: OP_DONT_SHADE, Name: "DONT_SHADE", Operands: 1},
		{Opcode: OP_EMIT_SFX, Name: "EMIT_SFX", Operands: 1, Pops: 1},
		// Wait
		{Opcode: OP_WAIT_FOR_TURN, Name: "WAIT_FOR_TURN", Operands: 2},
		{Opcode: OP_WAIT_FOR_MOVE, Name: "WAIT_FOR_MOVE", Operands: 2},
		{Opcode: OP_SLEEP, Name: "SLEEP", Pops: 1},
		// Stack
		{Opcode: opPushBase, Name: "PUSH_IMM", Operands: 1, Pushes: 1, Faults: true},
		{Opcode: OP_PUSH_CONSTANT, Name: "PUSH_CONST", Operands: 1, Pushes: 1},
		{Opcode: OP_PUSH_LOCAL_VAR, Name: "PUSH_LOCAL", Operands: 1, Pushes: 1},
		{Opcode: OP_PUSH_STATIC, Name: "PUSH_STATIC", Operands: 1, Pushes: 1},
		{Opcode: OP_STACK_ALLOC, Name: "STACK_ALLOC", Pushes: 1},
		{Opcode: OP_POP_LOCAL_VAR, Name: "POP_LOCAL", Operands: 1, Pops: 1},
		{Opcode: OP_POP_STATIC, Name: "POP_STATIC", Operands: 1, Pops: 1},
		{Opcode: OP_POP_STACK, Name: "POP_STACK", Pops: 1},
		// Arithmetic and bitwise
		{Opcode: OP_ADD, Name: "ADD", Pops: 2, Pushes: 1},
		{Opcode: OP_SUB, Name: "SUB", Pops: 2, Pushes: 1},
		{Opcode: OP_MUL, Name: "MUL", Pops: 2, Pushes: 1},
		{Opcode: OP_DIV, Name: "DIV", Pops: 2, Pushes: 1},
		{Opcode: OP_BITWISE_AND, Name: "BITWISE_AND", Pops: 2, Pushes: 1},
		{Opcode: OP_BITWISE_OR, Name: "BITWISE_OR", Pops: 2, Pushes: 1},
		{Opcode: OP_XOR, Name: "XOR", Pops: 2, Pushes: 1},
		{Opcode: OP_NOT, Name: "NOT", Pops: 1, Pushes: 1},
		{Opcode: OP_TAK_MATH_09, Name: "TAK_MATH_09", Pops: 2, Pushes: 1, Kingdoms: true},
		{Opcode: OP_TAK_MATH_0A, Name: "TAK_MATH_0A", Pops: 2, Pushes: 1, Kingdoms: true},
		{Opcode: OP_TAK_MATH_0B, Name: "TAK_MATH_0B", Pops: 2, Pushes: 1, Kingdoms: true},
		// Queries
		{Opcode: OP_RAND, Name: "RAND", Pops: 2, Pushes: 1},
		{Opcode: OP_GET_UNIT_VALUE, Name: "GET_UNIT_VALUE", Pops: 1, Pushes: 1},
		{Opcode: OP_GET, Name: "GET", Pops: 5, Pushes: 1},
		{Opcode: OP_IS_CARRYING_UNIT, Name: "IS_CARRYING_UNIT", Pops: 1, Pushes: 1},
		{Opcode: OP_CARRIER_UNIT_ID, Name: "CARRIER_UNIT_ID", Pushes: 1},
		// Comparison and logical
		{Opcode: OP_LESS_THAN, Name: "LESS_THAN", Pops: 2, Pushes: 1},
		{Opcode: OP_LESS_OR_EQUAL, Name: "LESS_OR_EQUAL", Pops: 2, Pushes: 1},
		{Opcode: OP_GREATER_THAN, Name: "GREATER_THAN", Pops: 2, Pushes: 1},
		{Opcode: OP_GREATER_EQUAL, Name: "GREATER_EQUAL", Pops: 2, Pushes: 1},
		{Opcode: OP_EQUAL, Name: "EQUAL", Pops: 2, Pushes: 1},
		{Opcode: OP_NOT_EQUAL, Name: "NOT_EQUAL", Pops: 2, Pushes: 1},
		{Opcode: OP_LOGICAL_AND, Name: "LOGICAL_AND", Pops: 2, Pushes: 1},
		{Opcode: OP_LOGICAL_OR, Name: "LOGICAL_OR", Pops: 2, Pushes: 1},
		{Opcode: OP_XOR_ALT, Name: "XOR_ALT", Pops: 2, Pushes: 1},
		{Opcode: OP_LOGICAL_NOT, Name: "LOGICAL_NOT", Pops: 1, Pushes: 1},
		// Control flow
		{Opcode: OP_START_SCRIPT, Name: "START_SCRIPT", Operands: 2, Pops: VariablePops},
		{Opcode: OP_CALL_SCRIPT, Name: "CALL_SCRIPT", Operands: 2, Pops: VariablePops},
		{Opcode: OP_DISCARD_CALL, Name: "DISCARD_CALL", Operands: 2, Pops: VariablePops},
		{Opcode: OP_JUMP, Name: "JUMP", Operands: 1},
		{Opcode: OP_RETURN, Name: "RETURN"},
		{Opcode: OP_JUMP_IF_FALSE, Name: "JUMP_IF_FALSE", Operands: 1, Pops: 1},
		{Opcode: OP_SIGNAL, Name: "SIGNAL", Pops: 1},
		{Opcode: OP_SET_SIGNAL_MASK, Name: "SET_SIGNAL_MASK", Pops: 1},
		// Effects
		{Opcode: OP_EXPLODE, Name: "EXPLODE", Operands: 1, Pops: 1},
		{Opcode: OP_PLAY_SOUND, Name: "PLAY_SOUND", Operands: 1, Pops: 1, Pushes: 1, Kingdoms: true},
		{Opcode: OP_MISSION_COMMAND, Name: "MISSION_COMMAND", Operands: 2, Pops: VariablePops, Pushes: 1, Kingdoms: true},
		// Unit values and transport
		{Opcode: OP_SET_VALUE, Name: "SET_VALUE", Pops: 2},
		{Opcode: OP_ATTACH_UNIT, Name: "ATTACH_UNIT", Pops: 3},
		{Opcode: OP_DROP_UNIT, Name: "DROP_UNIT", Pops: 1},
	}
	table := make(map[uint32]OpcodeInfo, len(infos))
	for _, info := range infos {
		table[info.Opcode] = info
	}
	return table
}()

// legacyMnemonics are names written by earlier disassemblers. Each maps to
// the word those listings meant, so an old listing assembles to the same
// bytes; several of them name the wrong operation (see the deprecated
// constants).
var legacyMnemonics = map[string]uint32{
	"MOD":              OP_XOR,
	"BITWISE_XOR":      OP_NOT,
	"BITWISE_NOT":      OP_TAK_MATH_0A,
	"LOGICAL_XOR":      OP_XOR_ALT,
	"CREATE_LOCAL":     0x10021008,
	"PUSH_CONSTANT":    OP_PUSH_CONSTANT,
	"PUSH_IMMEDIATE":   opPushBase,
	"PUSH_LOCAL_VAR":   OP_PUSH_LOCAL_VAR,
	"POP_LOCAL_VAR":    OP_POP_LOCAL_VAR,
	"GREATER_OR_EQUAL": OP_GREATER_EQUAL,
}

var mnemonicTable = func() map[string]uint32 {
	table := make(map[string]uint32, len(opcodeTable)+len(legacyMnemonics))
	for op, info := range opcodeTable {
		table[info.Name] = op
	}
	for name, op := range legacyMnemonics {
		if _, taken := table[name]; !taken {
			table[name] = op
		}
	}
	return table
}()

// DispatchOpcode returns the canonical encoding of the instruction the game
// runs for a raw opcode word: the word masked with OpcodeDispatchMask, plus
// the low three flag bits for PUSH and POP. For every word a compiler
// normally writes the result equals the word itself.
func DispatchOpcode(raw uint32) uint32 {
	op := raw & OpcodeDispatchMask
	if op == opPushBase || op == opPopBase {
		return op | raw&opFlagMask
	}
	return op
}

// LookupOpcode describes the instruction the game runs for a raw word;
// low-bit variants resolve to their base instruction. It reports false for
// words that neither TA nor TA: Kingdoms executes, including PUSH and POP
// with a flag the game rejects (the flagless PUSH is listed, with Faults
// set).
func LookupOpcode(raw uint32) (OpcodeInfo, bool) {
	info, ok := opcodeTable[DispatchOpcode(raw)]
	return info, ok
}

// Opcodes returns every instruction the table describes, ordered by
// opcode, for callers that generate their own tables from it.
func Opcodes() []OpcodeInfo {
	out := make([]OpcodeInfo, 0, len(opcodeTable))
	for _, info := range opcodeTable {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Opcode < out[j].Opcode })
	return out
}

// OpcodeName returns the mnemonic of the instruction the game runs for a raw
// or canonical opcode word. Low-bit variants get their base instruction's
// name. Words no game executes are named UNKNOWN_0xXXXXXXXX with their
// canonical value, a form OpcodeByName reads back.
func OpcodeName(opcode uint32) string {
	canonical := DispatchOpcode(opcode)
	if info, ok := opcodeTable[canonical]; ok {
		return info.Name
	}
	return fmt.Sprintf("UNKNOWN_0x%08X", canonical)
}

// OpcodeHasInlineParam reports whether the opcode is followed by inline
// operand words (OpcodeParamCount above zero).
func OpcodeHasInlineParam(opcode uint32) bool {
	return OpcodeParamCount(opcode) > 0
}

// OpcodeParamCount returns how many operand words (0, 1 or 2) follow a raw
// or canonical opcode word, using the game's dispatch rule. PUSH and POP
// take one operand whatever their flag bits; words no game executes take
// none.
func OpcodeParamCount(opcode uint32) int {
	switch opcode & OpcodeDispatchMask {
	case opPushBase, opPopBase:
		return 1
	}
	if info, ok := opcodeTable[DispatchOpcode(opcode)]; ok {
		return info.Operands
	}
	return 0
}

// DecodePackedOperand extracts components from packed animation opcodes
// Format: [piece_id:8][axis:8][speed_or_value:8][flags:8]
func DecodePackedOperand(operand uint32) (pieceID, axis, value, flags uint8) {
	flags = uint8(operand >> 24)
	value = uint8(operand >> 16)
	axis = uint8(operand >> 8)
	pieceID = uint8(operand)
	return
}

// EncodePackedOperand packs piece, axis, value, flags into a single uint32
func EncodePackedOperand(pieceID, axis, value, flags uint8) uint32 {
	return uint32(flags)<<24 | uint32(value)<<16 | uint32(axis)<<8 | uint32(pieceID)
}

// AxisName converts axis code to string
func AxisName(axis uint8) string {
	switch axis {
	case 0:
		return "x-axis"
	case 1:
		return "y-axis"
	case 2:
		return "z-axis"
	default:
		return fmt.Sprintf("axis_%d", axis)
	}
}

// OpcodeByName returns the opcode word to write for a mnemonic.
//
// It accepts the names OpcodeName returns, the names earlier disassemblers
// wrote (MOD, BITWISE_XOR, BITWISE_NOT, LOGICAL_XOR, CREATE_LOCAL and the
// long stack-operation names), each mapped to the word those listings
// meant, and three raw forms: NAME@0xXXXXXXXX for a low-bit variant of NAME,
// UNKNOWN_0xXXXXXXXX and a bare 0xXXXXXXXX. Names are case-sensitive. It
// reports false when the name is not recognised or a NAME@0x... word does
// not dispatch as NAME.
func OpcodeByName(name string) (uint32, bool) {
	if base, raw, found := strings.Cut(name, "@"); found {
		word, ok := parseOpcodeWord(raw)
		if !ok {
			return 0, false
		}
		named, ok := OpcodeByName(base)
		if !ok || DispatchOpcode(named) != DispatchOpcode(word) {
			return 0, false
		}
		return word, true
	}
	if op, ok := mnemonicTable[name]; ok {
		return op, true
	}
	if hex, found := strings.CutPrefix(name, "UNKNOWN_"); found {
		return parseOpcodeWord(hex)
	}
	return parseOpcodeWord(name)
}

// parseOpcodeWord parses a 0x-prefixed hexadecimal opcode word.
func parseOpcodeWord(s string) (uint32, bool) {
	digits, found := strings.CutPrefix(s, "0x")
	if !found {
		digits, found = strings.CutPrefix(s, "0X")
	}
	if !found || digits == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(digits, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}
