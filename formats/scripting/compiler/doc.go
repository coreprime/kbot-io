// Package compiler compiles BOS source to COB bytecode.
//
// The compiler never writes code the game would mis-run: constructs it
// cannot express are errors, not placeholders. In particular:
//
//   - unknown identifiers are errors; piece names evaluate to their index,
//     and the unit-value port names (HEALTH, PIECE_XZ, ...) to their number;
//   - `^` compiles to the bitwise XOR (0x10037000), `~` to the bitwise NOT
//     (0x10038000), `&` to AND and the keyword XOR to the game's second XOR
//     (0x10059000); AND, OR and NOT are the logical operators;
//   - `%` is an error in TA scripts: TA has no modulo instruction, and
//     0x10037000, the word `%` used to compile to, is its bitwise XOR. Under
//     `.version 6` `%` still compiles to 0x10037000 and Compiler.Warnings
//     notes that what TA: Kingdoms does with that instruction is not
//     established;
//   - GET always pops a port and four arguments, so `get(port)`,
//     `get PORT(a)` and the like push zeros for the missing arguments;
//   - a function may have at most 32 parameters and locals, and the locals
//     plus the deepest expression must fit the game's 32 stack slots;
//   - play-sound, Mission-Command, the __tak_math_* intrinsics and
//     `.sound_name` are TA: Kingdoms instructions that TA faults on; they
//     compile only after `.version 6`;
//   - when a function name is defined twice, calls bind to the first
//     definition, as the game's name lookup does, and Compiler.Warnings
//     reports it;
//   - a function whose code does not end with RETURN gets `return 0`
//     appended, as the retail compiler does, instead of running on into the
//     next function.
//
// Angles are written <degrees> and distances [units], as in the retail
// sources: <35> is 6371 and [2.4] is 393216 (65536 per 360 degrees and
// 163840 per unit, truncated toward zero). Earlier versions of the
// decompiler wrote raw values inside angle brackets; `.angle_units raw`
// restores that reading, and it is assumed for sources that start with the
// banner those versions wrote.
//
// Top-of-file directives: `.version N`, `.sound_name "..."`, `.field5 N`
// (header field 5) and `.angle_units degrees|raw`. Instructions without a
// BOS keyword use the intrinsics __discard_call(word, args...),
// __piece_op_09(piece, a, b), __is_carrying_unit(unit) and
// __carrier_unit_id(), which the decompiler writes.
package compiler
