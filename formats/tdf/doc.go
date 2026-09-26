// Package tdf reads and writes the text format shared by Total Annihilation's
// TDF, FBI, GUI and OTA files (and TA: Kingdoms' text data).
//
// A file is a list of statements. "[NAME] { ... }" is a section holding more
// statements; "key=value;" is a field. Keys and section names match ignoring
// ASCII case.
//
//	// Reading into a tree
//	doc, err := tdf.ParseFile("units/ARMCOM.FBI")
//	unit := doc.Section("UNITINFO")
//	name := unit.String("UnitName")
//	cost := unit.Int("BuildCostMetal")
//
//	// Reading into structs
//	var u ta.Unit
//	err = tdf.Unmarshal(data, &u)
//
//	// Writing
//	doc := tdf.NewDocument()
//	unit := doc.AddSection("UNITINFO")
//	unit.SetString("UnitName", "ARMCOM")
//	unit.SetInt("BuildCostMetal", 2500)
//	err = doc.WriteFile("output.fbi")
//
// # Grammar
//
// Every reader in this package (Parse, Unmarshal, Decoder, Canonicalize,
// SemanticEqual and Diagnose) shares one tokenizer, which reads text the way
// TA 3.1c does:
//
//   - The text ends at the end of the input or at the first NUL byte; bytes
//     after a NUL are never read.
//   - Comments are replaced by spaces, byte for byte, before anything else
//     is read: "//" to the end of its line (the line break stays) and "/*"
//     to the next "*/", including inside values and section names. An
//     unterminated "/*" blanks everything to the end of the text except the
//     final byte, which the game keeps and reads as text.
//   - Only space, tab, CR and LF separate tokens. Form feed, vertical tab,
//     the DOS end-of-file byte 0x1A and a UTF-8 byte order mark are ordinary
//     text: a leading byte order mark becomes part of the first statement, so
//     a file saved with one loses its first section in the game.
//   - A statement starting with '[' is a section: its name runs to the next
//     ']' and must be followed by '{'. A '}' ends the current section. A '}'
//     outside any section ends the text: the game ignores everything after
//     it, and so does this package.
//   - Any other statement is a field. Its key runs to the next '=' and its
//     value to the next ';', wherever they are: across line breaks, braces
//     and brackets. A value is never ended by a line break or a '}'. A
//     missing ';' therefore makes the value swallow the text up to the next
//     ';', and stray text (such as a doubled ';') becomes part of the next
//     key. Retail files have both, and the game loses the fields concerned;
//     so does this package.
//   - Keys, values and section names are trimmed of spaces, tabs, CRs and
//     LFs at both ends, and of nothing else.
//   - A key assigned more than once in a section keeps its last value,
//     whatever the case of each assignment. Sections are kept in order, and
//     a lookup by name finds the first section with that name.
//
// The game refuses a whole file for some conditions: a section header with no
// '{', text with no '=' or a value with no ';' before the end of the file, the
// end of the file inside a section, and (at the file level) empty input. By
// default this package repairs these so damaged files still load: the header
// becomes an empty section, the trailing text is dropped or the value runs to
// the end, and open sections are closed. ParseOptions.Strict refuses them
// instead, with a *SyntaxError giving the byte offset. Diagnose,
// Document.Diagnostics and ParseOptions.OnDiagnostic report every such
// condition, and others the game accepts but that are usually mistakes (a
// value spanning a line break or containing a brace, a stray '}', duplicate
// sections and keys, a leading byte order mark, a NUL byte, an unterminated
// comment).
//
// ParseOptions.SkipStrayText drops text that reaches ';', '{', '}' or '[' before
// any '=' instead of gluing it onto the next key, for data whose own reader is
// known to skip it.
//
// Input is limited to DefaultMaxBytes of text and DefaultMaxDepth levels of
// nested sections; ParseOptions changes both. The game has no such limits;
// these bound memory and recursion for hostile input.
//
// # Numbers and flags
//
// The game never rejects a value for its syntax. Atol reads integers (leading
// digits after an optional sign, wrapping to 32 bits: "12abc" is 12, "1.9" is
// 1), Atof reads floats ([sign] digits [. digits] [e|E|d|D exponent], nothing
// else: "13O" is 13, "1.5d2" is 150, "inf" is 0), Fixed reads the game's 16.16
// fixed-point fields and Flag reads booleans as bit 0 of Atol ("3" is true,
// "2" and "true" are false). Section's Int, Float, Fixed and Bool and the
// struct codec use them, so a value reads the same everywhere.
//
// # Struct codec
//
// Unmarshal, Marshal, Decoder and Encoder map documents onto tagged structs
// (see Unmarshal for the tag forms). A struct field matching several sections
// takes the first; map entries and catch-all keys that differ only in case
// merge into one holding the last value. A value whose text would not be
// written back the same way (such as "13O", read as 13) also keeps its text in
// the struct's ",remaining" catch-all, so Marshal reproduces it.
//
// Writers refuse keys, values and section names the grammar cannot carry
// (see CheckKey, CheckValue and CheckName): the grammar has no escaping, so a
// ';' in a value, a "//" in a URL or a ']' in a section name would change what
// the game reads.
//
// # Rewriting
//
// A rewrite by Marshal, Canonicalize or Document.Write normalises layout and
// drops comments. The game hashes the raw bytes of some files (FBI files, and
// the comment-blanked text of weapon sections and of an OTA's [GlobalHeader])
// to check that players share the same data, so such a rewrite changes those
// hashes even when every value is the same: a rewritten unit set is a
// different unit set to the game.
package tdf
