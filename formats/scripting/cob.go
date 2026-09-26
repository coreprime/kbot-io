package scripting

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/coreprime/kbot-io/filesystem"
)

// COB represents a compiled BOS script (Total Annihilation format).
type COB struct {
	// Header fields (11 DWORDs for TA's VersionSignature=4 files; TA: Kingdoms
	// uses VersionSignature=6 and inserts an 8-byte sub-header before the code
	// section plus a per-COB sound-name table after the piece names — both
	// are reconstructed from the structured fields below, not preserved as
	// opaque bytes.)
	VersionSignature              uint32 // [0] Version (4 for TA, 6 for TA: Kingdoms)
	NumScripts                    uint32 // [1] Number of scripts
	NumPieces                     uint32 // [2] Number of pieces
	LengthOfScripts               uint32 // [3] Total code size in DWORDs (recomputed from Code on write)
	NumberOfStaticVars            uint32 // [4] Number of static variables
	UKZero                        uint32 // [5] Always 0 in retail bytecode; purpose unknown; written back unchanged
	OffsetToScriptCodeIndexArray  uint32 // [6] Offset to script index array
	OffsetToScriptNameOffsetArray uint32 // [7] Offset to script name array (ABSOLUTE offsets)
	OffsetToPieceNameOffsetArray  uint32 // [8] Offset to piece name array (ABSOLUTE offsets)
	OffsetToScriptCode            uint32 // [9] Offset to script code
	OffsetToNameArray             uint32 // [10] Start of the trailing offset/string region (begins at the sound-name offset table for v6, otherwise the script-name pool start)

	// Parsed data
	Code              []byte
	ScriptCodeIndices []uint32 // Indices from OffsetToScriptCodeIndexArray
	ScriptNames       []string
	PieceNames        []string

	// SoundNames is the TA: Kingdoms–only addendum to the string pool.
	// v6 .cob files insert a `len(SoundNames)` × uint32 offset table
	// after the piece-name offset array (pointed at by the 8-byte extra
	// sub-header at file offset 0x2C), followed by the actual strings in
	// order. The MISSION_COMMAND opcode (0x10073000) references them by
	// index via its first inline DWORD; the offset table and sub-header
	// are reconstructed from this slice on write — for TA's v4 .cob files
	// the slice is always nil and the wrapping pieces are omitted.
	SoundNames []string

	// Warnings lists the malformed parts of the file that LoadFromReader
	// tolerated: name offsets outside the name pool or past the end of the
	// file, names without a terminating NUL, and header fields that
	// disagree with the section layout. A rewrite does not repair them.
	Warnings []LoadWarning
}

// LoadWarning describes one malformed but tolerated part of a COB file.
type LoadWarning struct {
	Section string // "script names", "piece names", "sound names" or "header"
	Index   int    // entry index within the section, or -1
	Offset  uint32 // file offset concerned
	Message string
}

func (w LoadWarning) String() string {
	if w.Index >= 0 {
		return fmt.Sprintf("%s[%d] at 0x%X: %s", w.Section, w.Index, w.Offset, w.Message)
	}
	return fmt.Sprintf("%s at 0x%X: %s", w.Section, w.Offset, w.Message)
}

// ErrTruncatedInstruction reports an instruction whose opcode or operand
// words run past the end of the code section.
var ErrTruncatedInstruction = errors.New("truncated instruction")

const (
	cobHeaderSize         = 44
	cobKingdomsSubHeader  = 8
	cobKingdomsVersion    = 6
	cobMaxWrittenFileSize = math.MaxUint32
)

// LoadFromFile reads a COB file from the local filesystem
func LoadFromFile(path string) (*COB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	return LoadFromReader(f)
}

// LoadFromFilesystem reads a COB file from a virtual filesystem
func LoadFromFilesystem(fs filesystem.FileSystem, path string) (*COB, error) {
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return LoadFromReader(bytes.NewReader(data))
}

// LoadFromReader reads a COB from an io.Reader.
//
// The game turns the header offsets into pointers without checking the
// layout, so the reader accepts any arrangement of sections that lies
// inside the file. The code section runs from its offset to the next table
// that follows it (or the end of the file); a file without code is
// accepted. Every table is bounds-checked against the file size before
// anything is allocated, so a count or offset that cannot fit returns an
// error. Name offsets are resolved like the game resolves them, as a
// NUL-terminated string at that file offset (offset 0 names the header
// bytes); offsets outside the name pool, past the end of the file or
// without a terminating NUL are recorded in COB.Warnings.
func LoadFromReader(r io.Reader) (*COB, error) {
	cob := &COB{}

	allData, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	size := uint64(len(allData))

	if size < cobHeaderSize {
		return nil, fmt.Errorf("file too small for header")
	}

	u32 := func(off uint64) uint32 { return binary.LittleEndian.Uint32(allData[off : off+4]) }
	cob.VersionSignature = u32(0)
	cob.NumScripts = u32(4)
	cob.NumPieces = u32(8)
	cob.LengthOfScripts = u32(12)
	cob.NumberOfStaticVars = u32(16)
	cob.UKZero = u32(20)
	cob.OffsetToScriptCodeIndexArray = u32(24)
	cob.OffsetToScriptNameOffsetArray = u32(28)
	cob.OffsetToPieceNameOffsetArray = u32(32)
	cob.OffsetToScriptCode = u32(36)
	cob.OffsetToNameArray = u32(40)

	// TA: Kingdoms .cob files (VersionSignature == 6) insert an 8-byte
	// sub-header at file offset 0x2C: two little-endian uint32s holding
	// the absolute offset of the sound-name offset table and the number
	// of sound names. The table itself is read from just past the
	// piece-name offset array, which is where every retail file has it.
	var soundNameCount, soundTableHint uint32
	if cob.VersionSignature == cobKingdomsVersion && cob.OffsetToScriptCode >= cobHeaderSize+cobKingdomsSubHeader &&
		size >= cobHeaderSize+cobKingdomsSubHeader {
		soundTableHint = u32(44)
		soundNameCount = u32(48)
	}

	readTable := func(section string, off uint32, count uint32) ([]uint32, error) {
		end := uint64(off) + uint64(count)*4
		if end > size {
			return nil, fmt.Errorf("%s table (%d entries at 0x%X) runs past the end of the %d-byte file",
				section, count, off, size)
		}
		out := make([]uint32, count)
		for i := range out {
			out[i] = u32(uint64(off) + uint64(i)*4)
		}
		return out, nil
	}

	// Entry and name tables first: they bound the counts before anything
	// else is allocated from them.
	if cob.ScriptCodeIndices, err = readTable("script entry", cob.OffsetToScriptCodeIndexArray, cob.NumScripts); err != nil {
		return nil, err
	}
	scriptNameOffsets, err := readTable("script name", cob.OffsetToScriptNameOffsetArray, cob.NumScripts)
	if err != nil {
		return nil, err
	}
	pieceNameOffsets, err := readTable("piece name", cob.OffsetToPieceNameOffsetArray, cob.NumPieces)
	if err != nil {
		return nil, err
	}
	var soundNameOffsets []uint32
	soundTable := uint64(cob.OffsetToPieceNameOffsetArray) + uint64(cob.NumPieces)*4
	if soundNameCount > 0 {
		if soundTable > math.MaxUint32 {
			return nil, fmt.Errorf("sound name table offset 0x%X is out of range", soundTable)
		}
		if soundNameOffsets, err = readTable("sound name", uint32(soundTable), soundNameCount); err != nil {
			return nil, err
		}
		if uint64(soundTableHint) != soundTable {
			cob.warn("header", -1, soundTableHint, fmt.Sprintf(
				"sound name table offset 0x%X differs from the table's position 0x%X after the piece names",
				soundTableHint, soundTable))
		}
	}

	// Code section: from its offset to the next table after it.
	codeStart := uint64(cob.OffsetToScriptCode)
	if codeStart > size {
		return nil, fmt.Errorf("code offset 0x%X is past the end of the %d-byte file", codeStart, size)
	}
	codeEnd := size
	bound := func(off uint32, present bool) {
		if present && uint64(off) >= codeStart && uint64(off) < codeEnd {
			codeEnd = uint64(off)
		}
	}
	bound(cob.OffsetToScriptCodeIndexArray, cob.NumScripts > 0)
	bound(cob.OffsetToScriptNameOffsetArray, cob.NumScripts > 0)
	bound(cob.OffsetToPieceNameOffsetArray, cob.NumPieces > 0)
	if soundNameCount > 0 {
		bound(uint32(soundTable), true)
	}
	bound(cob.OffsetToNameArray, cob.NumScripts > 0 || cob.NumPieces > 0 || soundNameCount > 0)
	cob.Code = allData[codeStart:codeEnd]
	if codeLen := codeEnd - codeStart; uint64(cob.LengthOfScripts)*4 != codeLen {
		cob.warn("header", -1, 12, fmt.Sprintf(
			"code length %d words disagrees with the %d-byte code section", cob.LengthOfScripts, codeLen))
	}

	readNames := func(section string, offsets []uint32) []string {
		names := make([]string, len(offsets))
		for i, off := range offsets {
			names[i] = cob.readName(allData, section, i, off)
		}
		return names
	}
	cob.ScriptNames = readNames("script names", scriptNameOffsets)
	cob.PieceNames = readNames("piece names", pieceNameOffsets)

	// TA: Kingdoms v6 .cob files append an extra offset table immediately
	// after the piece-name offset array (entry count from the sub-header
	// captured above) followed by the strings those offsets point at. The
	// strings are referenced from the bytecode by index and carry things
	// like spawn-target unit codes ("ARROW10", "ARAPRIESDIE1") for unit
	// scripts and engine-command strings ("SetMission o 1, s") for the
	// mission COBs — picked up by the MISSION_COMMAND opcode.
	if soundNameCount > 0 {
		cob.SoundNames = readNames("sound names", soundNameOffsets)
	}

	return cob, nil
}

// readName resolves one name offset the way the game does and records a
// warning when the offset is malformed.
func (c *COB) readName(data []byte, section string, index int, off uint32) string {
	if uint64(off) >= uint64(len(data)) {
		c.warn(section, index, off, "name offset is past the end of the file")
		return ""
	}
	if off < c.OffsetToNameArray {
		c.warn(section, index, off, fmt.Sprintf("name offset is before the name pool at 0x%X", c.OffsetToNameArray))
	}
	rest := data[off:]
	if n := bytes.IndexByte(rest, 0); n >= 0 {
		return string(rest[:n])
	}
	c.warn(section, index, off, "name is not NUL-terminated before the end of the file")
	return string(rest)
}

func (c *COB) warn(section string, index int, off uint32, msg string) {
	c.Warnings = append(c.Warnings, LoadWarning{Section: section, Index: index, Offset: off, Message: msg})
}

// Instruction represents a single COB bytecode instruction (nTA format)
type Instruction struct {
	Offset   uint32
	Opcode   uint32 // Canonical opcode the game runs (see DispatchOpcode)
	Operand  int32  // First 32-bit parameter (for 1-param opcodes)
	Operand2 int32  // Second 32-bit parameter (for 2-param opcodes like TURN)
	// Raw is the opcode word as stored. It differs from Opcode only for
	// low-bit variants such as 0x10064001, which the game runs as JUMP.
	// Zero means "same as Opcode".
	Raw uint32
}

// Word returns the opcode word to write for the instruction: Raw, or Opcode
// when Raw is zero.
func (i Instruction) Word() uint32 {
	if i.Raw != 0 {
		return i.Raw
	}
	return i.Opcode
}

// Mnemonic returns the assembler name of the instruction: the opcode name,
// followed by "@" and the stored word when that word is a low-bit variant.
// OpcodeByName reads the result back to the same word.
func (i Instruction) Mnemonic() string {
	name := OpcodeName(i.Opcode)
	if word := i.Word(); word != DispatchOpcode(word) {
		return fmt.Sprintf("%s@0x%08X", name, word)
	}
	return name
}

// StackEffect returns how many values the instruction pops and pushes when
// the game (or, for Kingdoms instructions, TA: Kingdoms) runs it. It reports
// false for words neither game executes.
func (i Instruction) StackEffect() (pops, pushes int, ok bool) {
	info, ok := LookupOpcode(i.Word())
	if !ok {
		return 0, 0, false
	}
	pops = info.Pops
	if pops == VariablePops {
		pops = int(i.Operand2)
	}
	return pops, info.Pushes, true
}

// Disassemble decodes the instructions of one script.
//
// A script runs from its entry word to the nearest entry of any other
// script that starts after it, or to the end of the code. Opcode words are
// decoded with the game's dispatch rule (DispatchOpcode); Raw keeps the
// stored word. When an instruction's opcode or operand words would run past
// the end of the code, Disassemble returns the instructions decoded before
// it together with an error wrapping ErrTruncatedInstruction.
func (c *COB) Disassemble(scriptIndex int) ([]Instruction, error) {
	if scriptIndex < 0 || scriptIndex >= int(c.NumScripts) || scriptIndex >= len(c.ScriptCodeIndices) {
		return nil, fmt.Errorf("invalid script index %d", scriptIndex)
	}

	codeLen := uint64(len(c.Code))
	start := uint64(c.ScriptCodeIndices[scriptIndex]) * 4
	if start >= codeLen {
		return nil, fmt.Errorf("invalid script offset 0x%X (code is %d bytes)", start, codeLen)
	}

	end := codeLen
	for _, entry := range c.ScriptCodeIndices {
		if off := uint64(entry) * 4; off > start && off < end {
			end = off
		}
	}

	word := func(pos uint64) uint32 { return binary.LittleEndian.Uint32(c.Code[pos : pos+4]) }

	var instructions []Instruction
	for pos := start; pos < end; {
		if pos+4 > codeLen {
			return instructions, fmt.Errorf("%w: partial opcode word at 0x%X", ErrTruncatedInstruction, pos)
		}
		raw := word(pos)
		paramCount := uint64(OpcodeParamCount(raw))
		if pos+4+paramCount*4 > codeLen {
			return instructions, fmt.Errorf("%w: %s at 0x%X needs %d operand words past the end of the code",
				ErrTruncatedInstruction, OpcodeName(raw), pos, paramCount)
		}
		inst := Instruction{Offset: uint32(pos), Opcode: DispatchOpcode(raw), Raw: raw}
		if paramCount >= 1 {
			inst.Operand = int32(word(pos + 4))
		}
		if paramCount >= 2 {
			inst.Operand2 = int32(word(pos + 8))
		}
		instructions = append(instructions, inst)
		pos += 4 + paramCount*4
	}

	return instructions, nil
}

// String returns a string representation of the instruction (TA COB format)
func (i Instruction) String() string {
	name := i.Mnemonic()
	if i.Operand != 0 || OpcodeHasInlineParam(i.Opcode) {
		return fmt.Sprintf("%04X: %-20s %d (0x%X)", i.Offset, name, i.Operand, i.Operand)
	}
	return fmt.Sprintf("%04X: %s", i.Offset, name)
}

// SaveToFile writes the COB to a file
func (c *COB) SaveToFile(filename string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return c.WriteToWriter(f)
}

// validateForWrite checks that the structured fields describe one
// consistent file.
func (c *COB) validateForWrite() error {
	if uint64(c.NumScripts) != uint64(len(c.ScriptCodeIndices)) {
		return fmt.Errorf("NumScripts is %d but there are %d script entries", c.NumScripts, len(c.ScriptCodeIndices))
	}
	if len(c.ScriptNames) != len(c.ScriptCodeIndices) {
		return fmt.Errorf("%d script names for %d script entries", len(c.ScriptNames), len(c.ScriptCodeIndices))
	}
	if uint64(c.NumPieces) != uint64(len(c.PieceNames)) {
		return fmt.Errorf("NumPieces is %d but there are %d piece names", c.NumPieces, len(c.PieceNames))
	}
	if len(c.SoundNames) > 0 && c.VersionSignature != cobKingdomsVersion {
		return fmt.Errorf("sound names need VersionSignature %d (TA: Kingdoms), not %d",
			cobKingdomsVersion, c.VersionSignature)
	}
	for _, group := range []struct {
		what  string
		names []string
	}{{"script", c.ScriptNames}, {"piece", c.PieceNames}, {"sound", c.SoundNames}} {
		for i, name := range group.names {
			if strings.IndexByte(name, 0) >= 0 {
				return fmt.Errorf("%s name %d contains a NUL byte", group.what, i)
			}
		}
	}
	return nil
}

// WriteToWriter writes the COB to a writer.
//
// The on-disk layout is fully reconstructed from the structured fields —
// no opaque-byte preservation. For TA's v4 dialect:
//
//	header (44) · code · script-code-index array · script-name offsets
//	  · piece-name offsets · string pool
//
// For TA: Kingdoms v6 .cob files an 8-byte sub-header (sound-name
// offset-table location + count) is inserted between the canonical header
// and the code section, and the trailing layout grows a sound-name
// offset table immediately after the piece-name array. The string pool
// is laid out script names → piece names → sound names, in that order,
// with all offsets and the v6 sub-header reconstructed from the
// structured fields below.
//
// The code is padded with zero bytes to a whole number of words and the
// header's code length is taken from it (LengthOfScripts is not consulted).
// NumScripts must equal the number of script entries and names, NumPieces
// the number of piece names; sound names need VersionSignature 6, and no
// name may contain a NUL byte. Header field 5 (UKZero) is written as is.
func (c *COB) WriteToWriter(w io.Writer) error {
	if err := c.validateForWrite(); err != nil {
		return fmt.Errorf("cannot write COB: %w", err)
	}

	code := c.Code
	if pad := len(code) % 4; pad != 0 {
		code = append(append([]byte(nil), code...), make([]byte, 4-pad)...)
	}

	headerSize := uint64(cobHeaderSize) // canonical TA 11-DWORD header
	subHeaderSize := uint64(0)
	if c.VersionSignature == cobKingdomsVersion {
		subHeaderSize = cobKingdomsSubHeader // [soundNameOffArr, len(SoundNames)]
	}
	codeOffset := headerSize + subHeaderSize
	codeSize := uint64(len(code))

	scriptCodeIndexOffset := codeOffset + codeSize
	scriptNameOffArr := scriptCodeIndexOffset + uint64(len(c.ScriptCodeIndices))*4
	pieceNameOffArr := scriptNameOffArr + uint64(len(c.ScriptNames))*4
	soundNameOffArr := pieceNameOffArr + uint64(len(c.PieceNames))*4
	stringPoolStart := soundNameOffArr + uint64(len(c.SoundNames))*4

	// String offsets in pool order: scripts → pieces → sound names.
	cursor := stringPoolStart
	layout := func(names []string) []uint32 {
		offsets := make([]uint32, len(names))
		for i, name := range names {
			offsets[i] = uint32(cursor)
			cursor += uint64(len(name)) + 1
		}
		return offsets
	}
	scriptOffsets := layout(c.ScriptNames)
	pieceOffsets := layout(c.PieceNames)
	soundOffsets := layout(c.SoundNames)
	if cursor > cobMaxWrittenFileSize {
		return fmt.Errorf("cannot write COB: %d bytes exceeds the 32-bit offset range", cursor)
	}

	header := make([]byte, headerSize)
	binary.LittleEndian.PutUint32(header[0:4], c.VersionSignature)
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(c.ScriptCodeIndices)))
	binary.LittleEndian.PutUint32(header[8:12], uint32(len(c.PieceNames)))
	binary.LittleEndian.PutUint32(header[12:16], uint32(codeSize/4))
	binary.LittleEndian.PutUint32(header[16:20], c.NumberOfStaticVars)
	binary.LittleEndian.PutUint32(header[20:24], c.UKZero)
	binary.LittleEndian.PutUint32(header[24:28], uint32(scriptCodeIndexOffset))
	binary.LittleEndian.PutUint32(header[28:32], uint32(scriptNameOffArr))
	binary.LittleEndian.PutUint32(header[32:36], uint32(pieceNameOffArr))
	binary.LittleEndian.PutUint32(header[36:40], uint32(codeOffset))
	// OffsetToNameArray always equals the byte just past the piece-name
	// offset array in retail TA + TAK files. For TA v4 that's the
	// string-pool start; for TA: Kingdoms v6 with sound names, it's the
	// start of the sound-name offset table (which only equals
	// stringPoolStart when len(SoundNames) == 0).
	binary.LittleEndian.PutUint32(header[40:44], uint32(soundNameOffArr))
	if _, err := w.Write(header); err != nil {
		return err
	}

	// TAK v6 sub-header: sound-name offset table location + count.
	if subHeaderSize > 0 {
		sub := make([]byte, subHeaderSize)
		binary.LittleEndian.PutUint32(sub[0:4], uint32(soundNameOffArr))
		binary.LittleEndian.PutUint32(sub[4:8], uint32(len(c.SoundNames)))
		if _, err := w.Write(sub); err != nil {
			return err
		}
	}

	if _, err := w.Write(code); err != nil {
		return err
	}

	writeOffsets := func(offsets []uint32) error {
		buf := make([]byte, 4*len(offsets))
		for i, off := range offsets {
			binary.LittleEndian.PutUint32(buf[i*4:], off)
		}
		_, err := w.Write(buf)
		return err
	}
	for _, offsets := range [][]uint32{c.ScriptCodeIndices, scriptOffsets, pieceOffsets, soundOffsets} {
		if err := writeOffsets(offsets); err != nil {
			return err
		}
	}

	writeStrings := func(strs []string) error {
		for _, s := range strs {
			if _, err := io.WriteString(w, s); err != nil {
				return err
			}
			if _, err := w.Write([]byte{0}); err != nil {
				return err
			}
		}
		return nil
	}
	for _, names := range [][]string{c.ScriptNames, c.PieceNames, c.SoundNames} {
		if err := writeStrings(names); err != nil {
			return err
		}
	}

	return nil
}
