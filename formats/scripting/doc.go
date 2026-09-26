// Package scripting reads, writes and decodes COB files, the compiled unit
// and mission scripts of Total Annihilation (version signature 4) and TA:
// Kingdoms (version signature 6). Its subpackages compile BOS source
// (compiler), decompile and disassemble COBs (decompiler, assembly) and lint
// them (linter).
//
// # File layout
//
// A COB starts with eleven little-endian words: version, script count,
// piece count, code length in words, static-variable count, header field
// 5, and the offsets of the script entry table, the script-name offset
// table, the piece-name offset table, the code and the name pool. TA:
// Kingdoms files insert an 8-byte sub-header (sound-name table offset and
// count) after the header and a sound-name offset table after the piece
// names.
//
// The game turns the header offsets into pointers without checking the
// layout, so LoadFromReader accepts any arrangement of sections that lies
// inside the file. The code runs from its offset to the next table after it
// (or the end of the file); a file with no code is accepted, which is what
// the compiler writes for a BOS without functions. Whether the game loads a
// script with no functions has not been established. Every table is checked
// against the file size before anything is allocated, so a corrupt count or
// offset is an error rather than a panic or a huge allocation. Names are
// read like the game reads them, as a NUL-terminated string at the stored
// file offset (offset 0 names the header bytes); offsets outside the name
// pool, past the end of the file or without a terminator are listed in
// COB.Warnings. Header field 5 (COB.UKZero) is zero in every retail file
// and its purpose is unknown; it is kept as read, and the BOS and assembly
// forms carry it as a `.field5` directive.
//
// WriteToWriter always writes the canonical layout (header, code, entry
// table, name tables, strings). It pads the code to whole words and takes
// the header's code length from it, and refuses structures whose counts
// disagree with their slices, sound names outside version 6 and names that
// contain a NUL byte.
//
// # Instructions
//
// Each instruction is an opcode word followed by up to two inline operand
// words. The game dispatches on raw & OpcodeDispatchMask, so bits outside
// the mask (as in 0x10064001) are ignored and the word runs as its base
// instruction; PUSH and POP then take their source or destination from the
// low three bits and fault on any flag other than 1, 2 or 4 (PUSH) and 2 or
// 4 (POP). COB.Disassemble decodes with this rule: Instruction.Opcode is the
// canonical opcode and Instruction.Raw the stored word, and the assembler
// writes a variant back as NAME@0xXXXXXXXX.
//
// Some published opcode tables mislabel the arithmetic group. The game
// runs 0x10037000 as bitwise XOR (OP_XOR), 0x10038000 as the unary bitwise
// NOT (OP_NOT) and 0x10059000 as a second bitwise XOR (OP_XOR_ALT); it has
// no modulo instruction and does not run 0x1003A000. The names OP_MOD,
// OP_BITWISE_XOR, OP_BITWISE_NOT and OP_LOGICAL_XOR keep their old values
// for existing callers and are deprecated. The table also covers the
// instructions without a BOS keyword: DISCARD_CALL (0x10063000, two inline
// words, pops the count in the second), PIECE_OP_09 (0x10009000, one inline
// piece, pops two), IS_CARRYING_UNIT (0x10044000) and CARRIER_UNIT_ID
// (0x10045000). LookupOpcode gives every instruction's operand count and
// stack effect.
//
// PLAY_SOUND, MISSION_COMMAND and the three TAK_MATH operators are TA:
// Kingdoms extensions (OpcodeInfo.Kingdoms). TA 3.1c has no handler for
// them and the script faults when it reaches one; kbot-io reads, writes and
// decompiles them for TA: Kingdoms files, but TA does not run them.
//
// # Stack
//
// A script context has StackSlots (32) slots shared by its local variables
// (one STACK_ALLOC each, parameters included) and pending expression
// values; the game does not check the limit. GET always pops a port and
// four arguments. AnalyzeStack follows a script's control flow and reports
// underflows, overflows and inconsistent joins; the compiler uses it to
// refuse code the game would mis-run and the linter's TA rules report it.
package scripting
