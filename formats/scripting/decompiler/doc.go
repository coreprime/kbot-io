// Package decompiler turns COB bytecode back into BOS source (Decompile)
// and into assembly listings (Disassemble).
//
// Decompile does not print BOS it knows would compile to different code.
// Jumps are written as if, if/else and while blocks, the only forms the
// compiler turns back into jumps; a jump that is not part of such a block
// (a bare forward or backward JUMP, or a JUMP_IF_FALSE whose target lies
// outside the enclosing block), an instruction with no BOS form and a pop
// of a value no expression produced make Decompile return an error. The
// disassembler shows such scripts exactly. Instructions stored as low-bit
// variants (for example 0x10064001) are written as the instruction the game
// runs, so they compile back to the canonical word.
package decompiler
