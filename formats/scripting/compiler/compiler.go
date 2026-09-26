package compiler

import (
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/coreprime/kbot-io/formats/scripting"
)

// Compiler compiles BOS source code to scripting.COB bytecode
type Compiler struct {
	source        string
	pieces        map[string]int
	pieceNames    []string
	statics       []string
	staticIndex   map[string]int
	scripts       []*CompiledScript
	scriptIndex   map[string]int
	currentScript *CompiledScript
	localIndex    map[string]int
	paramCount    int
	lastOpcode    uint32 // last instruction emitted into currentScript

	// COB metadata picked up from optional top-of-file BOS directives.
	// `.version` and `.sound_name "…"` are emitted by
	// `kbot cob decompile` so a TA: Kingdoms v6 .cob round-trips with the
	// right header version and per-COB sound-name table; legacy BOS files
	// without the directives default to TA's v4 layout with no sound
	// names.
	versionOverride int
	soundNames      []string
	field5          uint32
	angleUnits      angleUnits

	warnings []string
}

// angleUnits selects how the compiler reads a numeric <n> literal.
type angleUnits int

const (
	angleUnitsDefault angleUnits = iota // not set: degrees, unless the source is a legacy decompile
	angleUnitsDegrees                   // <n> is n degrees (65536 per turn)
	angleUnitsRaw                       // <n> is the integer n itself
)

// legacyDecompileBanner starts every BOS file written by earlier versions
// of the decompiler, which put raw values inside angle brackets.
const legacyDecompileBanner = "// Decompiled from COB bytecode"

// kingdomsVersion is the COB version signature of TA: Kingdoms scripts.
const kingdomsVersion = 6

// CompiledScript represents a compiled script
type CompiledScript struct {
	Name   string
	Code   []uint32 // Raw bytecode (opcodes and operands)
	Offset int      // Byte offset in code section
}

// functionSource is one function definition found in the source.
type functionSource struct {
	name   string
	params []string
	body   []string
}

// NewCompiler creates a new compiler
func NewCompiler(source string) *Compiler {
	return &Compiler{
		source:      source,
		pieces:      make(map[string]int),
		pieceNames:  []string{},
		statics:     []string{},
		staticIndex: make(map[string]int),
		scripts:     []*CompiledScript{},
		scriptIndex: make(map[string]int),
	}
}

// Warnings returns the non-fatal problems the last Compile call found, such
// as a function defined twice.
func (c *Compiler) Warnings() []string {
	return c.warnings
}

// Compile compiles BOS to scripting.COB.
//
// The output is always code the game can run: the compiler rejects
// constructs it cannot express correctly instead of emitting a placeholder.
// Unknown identifiers, functions with more than scripting.StackSlots local
// slots and expressions that need more stack than that are errors. TA:
// Kingdoms constructs (play-sound, Mission-Command, the __tak_math_*
// intrinsics and `.sound_name`) are accepted only after `.version 6`. The
// `%` operator is an error in TA scripts, which have no modulo instruction;
// under `.version 6` it compiles to 0x10037000 as before and Warnings notes
// that what TA: Kingdoms does with that instruction is not established.
// When a function name is defined twice both definitions are compiled,
// calls bind to the first (as the game's name lookup does), and Warnings
// reports the duplicate. A function whose code does not end with RETURN
// gets `return 0` appended.
func (c *Compiler) Compile() (*scripting.COB, error) {
	// Trailing // comments are dropped up front so that statements, braces
	// and `else` are recognised whatever follows them on the line.
	lines := strings.Split(c.source, "\n")
	for i, line := range lines {
		lines[i] = stripLineComment(line)
	}

	// Phase 1: Parse declarations
	if err := c.parseDeclarations(lines); err != nil {
		return nil, err
	}
	if len(c.soundNames) > 0 && !c.kingdoms() {
		return nil, fmt.Errorf(".sound_name is a TA: Kingdoms table; declare `.version %d` to use it", kingdomsVersion)
	}
	if c.angleUnits == angleUnitsDefault && firstLine(c.source) == legacyDecompileBanner {
		c.angleUnits = angleUnitsRaw
	}

	// Phase 2: Collect every function first so calls can refer to
	// functions defined later, then compile them in source order.
	funcs, err := c.collectFunctions(lines)
	if err != nil {
		return nil, err
	}
	for i, f := range funcs {
		if first, dup := c.scriptIndex[f.name]; dup {
			c.warnings = append(c.warnings, fmt.Sprintf(
				"function %s is defined more than once; calls and the game use the first definition (script %d)",
				f.name, first))
			continue
		}
		c.scriptIndex[f.name] = i
	}
	for _, f := range funcs {
		script, err := c.compileFunction(f.name, f.params, f.body)
		if err != nil {
			return nil, fmt.Errorf("compiling %s: %w", f.name, err)
		}
		c.scripts = append(c.scripts, script)
	}

	// Phase 3: Build scripting.COB structure
	return c.buildCOB(), nil
}

// version returns the COB version signature being compiled for.
func (c *Compiler) version() int {
	if c.versionOverride != 0 {
		return c.versionOverride
	}
	return 4
}

// kingdoms reports whether the script declares TA: Kingdoms (version 6).
func (c *Compiler) kingdoms() bool {
	return c.version() == kingdomsVersion
}

// requireKingdoms rejects a TA: Kingdoms construct in a TA script.
func (c *Compiler) requireKingdoms(construct string) error {
	if c.kingdoms() {
		return nil
	}
	return fmt.Errorf("%s is a TA: Kingdoms instruction that TA faults on; declare `.version %d` to compile it",
		construct, kingdomsVersion)
}

func firstLine(source string) string {
	for _, line := range strings.Split(source, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// parseDeclarations extracts piece and static-var declarations, plus the
// optional top-of-file metadata directives (see parseDirective).
func (c *Compiler) parseDeclarations(lines []string) error {
	pieceRE := regexp.MustCompile(`^piece\s+(.+);`)
	staticRE := regexp.MustCompile(`^static-var\s+(.+);`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Metadata directives (lifted from the assembler — see
		// formats/scripting/assembly/assembler.go for the matching
		// surface and rationale).
		if strings.HasPrefix(line, ".") {
			if err := c.parseDirective(line); err != nil {
				return err
			}
			continue
		}

		// Parse pieces (can be comma-separated)
		if m := pieceRE.FindStringSubmatch(line); m != nil {
			pieces := strings.Split(m[1], ",")
			for _, p := range pieces {
				name := strings.TrimSpace(p)
				c.pieces[name] = len(c.pieceNames)
				c.pieceNames = append(c.pieceNames, name)
			}
			continue
		}

		// Parse static vars
		if m := staticRE.FindStringSubmatch(line); m != nil {
			vars := strings.Split(m[1], ",")
			for _, v := range vars {
				v = strings.TrimSpace(v)
				c.staticIndex[v] = len(c.statics)
				c.statics = append(c.statics, v)
			}
			continue
		}
	}

	return nil
}

// parseDirective handles top-of-file BOS directives:
//
//	.version N          COB version signature (4 for TA, 6 for TA: Kingdoms)
//	.sound_name "..."   TA: Kingdoms sound-name table entry (version 6 only)
//	.field5 N           header field 5 (COB.UKZero), 0 when absent
//	.angle_units U      how a numeric <n> literal is read: degrees (the
//	                    default) or raw (the value itself, as earlier
//	                    decompilers wrote it)
func (c *Compiler) parseDirective(line string) error {
	parts := strings.SplitN(line, " ", 2)
	directive := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	switch directive {
	case ".version":
		v, err := strconv.Atoi(arg)
		if err != nil {
			return fmt.Errorf("bad .version value %q: %w", arg, err)
		}
		c.versionOverride = v
	case ".sound_name":
		s, err := strconv.Unquote(arg)
		if err != nil {
			return fmt.Errorf("bad .sound_name value %q: %w", arg, err)
		}
		c.soundNames = append(c.soundNames, s)
	case ".field5":
		v, err := strconv.ParseUint(arg, 0, 32)
		if err != nil {
			return fmt.Errorf("bad .field5 value %q: %w", arg, err)
		}
		c.field5 = uint32(v)
	case ".angle_units":
		switch arg {
		case "degrees":
			c.angleUnits = angleUnitsDegrees
		case "raw":
			c.angleUnits = angleUnitsRaw
		default:
			return fmt.Errorf("bad .angle_units value %q (want degrees or raw)", arg)
		}
	case ".statics":
		// Tolerated for symmetry with the assembler form. The actual
		// static count is derived from `static-var` declarations.
	case ".piece":
		// Same — `.piece` is the assembler form; BOS uses `piece a, b;`.
	default:
		return fmt.Errorf("unknown directive: %s", directive)
	}
	return nil
}

// collectFunctions finds every function definition and its body.
func (c *Compiler) collectFunctions(lines []string) ([]functionSource, error) {
	funcRE := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.*?)\)\s*$`)

	var funcs []functionSource
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])

		// Skip declarations, comments, empty
		if line == "" || strings.HasPrefix(line, "//") ||
			strings.HasPrefix(line, "piece ") || strings.HasPrefix(line, "static-var ") {
			i++
			continue
		}

		// Check for function signature
		if m := funcRE.FindStringSubmatch(line); m != nil {
			funcName := m[1]
			paramsStr := m[2]

			// Parse parameters
			params := []string{}
			if strings.TrimSpace(paramsStr) != "" {
				for _, p := range strings.Split(paramsStr, ",") {
					params = append(params, strings.TrimSpace(p))
				}
			}

			// Expect {
			i++
			if i >= len(lines) || strings.TrimSpace(lines[i]) != "{" {
				return nil, fmt.Errorf("expected '{' after %s()", funcName)
			}

			// Find matching }
			start := i + 1
			depth := 1
			i++
			for i < len(lines) && depth > 0 {
				l := strings.TrimSpace(lines[i])
				switch l {
				case "{":
					depth++
				case "}":
					depth--
				}
				i++
			}
			if depth > 0 {
				return nil, fmt.Errorf("missing '}' at the end of %s()", funcName)
			}
			end := i - 1

			funcs = append(funcs, functionSource{name: funcName, params: params, body: lines[start:end]})
			continue
		}

		i++
	}

	return funcs, nil
}

// compileFunction compiles a single function body
func (c *Compiler) compileFunction(name string, params []string, bodyLines []string) (*CompiledScript, error) {
	script := &CompiledScript{
		Name: name,
		Code: []uint32{},
	}

	c.currentScript = script
	c.lastOpcode = 0
	c.localIndex = make(map[string]int)
	c.paramCount = len(params)

	addLocal := func(v string) error {
		if !validIdentifier(v) {
			return fmt.Errorf("bad local variable name %q", v)
		}
		if _, dup := c.localIndex[v]; dup {
			return fmt.Errorf("local variable %s is declared twice", v)
		}
		c.localIndex[v] = len(c.localIndex)
		return nil
	}

	// Build local variable index (parameters first)
	for _, p := range params {
		if err := addLocal(p); err != nil {
			return nil, err
		}
	}

	// Parse local var declarations
	varRE := regexp.MustCompile(`^var\s+(.+);`)
	bodyStart := 0
	for idx, line := range bodyLines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			bodyStart = idx + 1
			continue
		}

		if m := varRE.FindStringSubmatch(line); m != nil {
			for _, v := range strings.Split(m[1], ",") {
				if err := addLocal(strings.TrimSpace(v)); err != nil {
					return nil, err
				}
			}
			bodyStart = idx + 1
			continue
		}
		break
	}

	// The game gives each script context scripting.StackSlots slots for
	// locals and pending values together; beyond that it overwrites the
	// memory after the context.
	if len(c.localIndex) > scripting.StackSlots {
		return nil, fmt.Errorf("%d parameters and local variables exceed the game's %d stack slots",
			len(c.localIndex), scripting.StackSlots)
	}

	// Emit STACK_ALLOC for ALL locals including parameters (matches original TA compiler)
	for i := 0; i < len(c.localIndex); i++ {
		c.emit(scripting.OP_STACK_ALLOC, 0)
	}

	// Compile body
	if err := c.compileBlock(bodyLines[bodyStart:]); err != nil {
		return nil, err
	}

	// A function whose code does not end with RETURN would run on into
	// the next function's code. End it like the retail compiler does, with
	// `return 0` (TA: Kingdoms: the value-less `return;` form), unless the
	// last instruction already is a RETURN. (As with the retail compiler, a
	// final `if` whose block ends in a return still lets the false branch
	// continue past the function.)
	if c.lastOpcode != scripting.OP_RETURN {
		if c.kingdoms() {
			c.emit(scripting.OP_STACK_ALLOC, 0)
		} else {
			c.emit(scripting.OP_PUSH_CONSTANT, 0)
		}
		c.emit(scripting.OP_RETURN, 0)
	}

	if err := c.checkStack(script); err != nil {
		return nil, err
	}
	return script, nil
}

// checkStack verifies that the compiled function never pops a value no
// expression pushed and stays within the game's stack slots.
func (c *Compiler) checkStack(script *CompiledScript) error {
	var insts []scripting.Instruction
	for pos := 0; pos < len(script.Code); {
		word := script.Code[pos]
		inst := scripting.Instruction{Offset: uint32(pos * 4), Opcode: scripting.DispatchOpcode(word), Raw: word}
		n := scripting.OpcodeParamCount(word)
		if n >= 1 && pos+1 < len(script.Code) {
			inst.Operand = int32(script.Code[pos+1])
		}
		if n >= 2 && pos+2 < len(script.Code) {
			inst.Operand2 = int32(script.Code[pos+2])
		}
		insts = append(insts, inst)
		pos += 1 + n
	}
	report := scripting.AnalyzeStack(insts, c.kingdoms())
	for _, issue := range report.Issues {
		switch issue.Kind {
		case scripting.StackOverflow:
			return fmt.Errorf("needs %d stack slots at 0x%04X; the game has %d",
				issue.Need, issue.Offset, scripting.StackSlots)
		case scripting.StackUnderflow:
			return fmt.Errorf("internal error: %s at 0x%04X pops %d values but only %d are pending",
				scripting.OpcodeName(issue.Opcode), issue.Offset, issue.Need, issue.Have)
		}
	}
	return nil
}

// emit appends an opcode and its inline operand words; operand fills the
// first one and any further ones are zero.
func (c *Compiler) emit(opcode uint32, operand int32) {
	c.lastOpcode = opcode
	c.currentScript.Code = append(c.currentScript.Code, opcode)
	for i := 0; i < scripting.OpcodeParamCount(opcode); i++ {
		if i == 0 {
			c.currentScript.Code = append(c.currentScript.Code, uint32(operand))
		} else {
			c.currentScript.Code = append(c.currentScript.Code, 0)
		}
	}
}

// emit2 emits an opcode with two operands (for opcodes like MOVE_NOW, TURN_NOW)
func (c *Compiler) emit2(opcode uint32, operand1 int32, operand2 int32) {
	c.lastOpcode = opcode
	c.currentScript.Code = append(c.currentScript.Code, opcode)
	c.currentScript.Code = append(c.currentScript.Code, uint32(operand1))
	c.currentScript.Code = append(c.currentScript.Code, uint32(operand2))
}

// emitPlaceholder emits a jump with placeholder operand, returns index for patching
func (c *Compiler) emitPlaceholder(opcode uint32) int {
	idx := len(c.currentScript.Code)
	c.emit(opcode, 0)
	return idx + 1 // Return operand index
}

// getPieceIndex looks up piece index by symbolic name. As a last resort it
// accepts the synthetic `piece_<N>` placeholders the decompiler emits when
// a referenced index lies past the COB's declared piece-name table — this
// happens in TA: Kingdoms mission COBs where the bytecode references piece
// slots that the script declares no name for.
func (c *Compiler) getPieceIndex(name string) (int, error) {
	if idx, ok := c.pieces[name]; ok {
		return idx, nil
	}
	if strings.HasPrefix(name, "piece_") {
		if idx, err := strconv.Atoi(name[len("piece_"):]); err == nil && idx >= 0 {
			return idx, nil
		}
	}
	return -1, fmt.Errorf("unknown piece: %s", name)
}

// parseAxis converts axis name to index (x-axis=0, y-axis=1, z-axis=2)
func parseAxis(axis string) (int, error) {
	axis = strings.ToLower(strings.TrimSpace(axis))
	switch axis {
	case "x-axis", "x":
		return 0, nil
	case "y-axis", "y":
		return 1, nil
	case "z-axis", "z":
		return 2, nil
	default:
		return -1, fmt.Errorf("invalid axis: %s", axis)
	}
}

// patchJump patches a jump instruction operand
func (c *Compiler) patchJump(operandIdx int, target int) {
	c.currentScript.Code[operandIdx] = uint32(target)
}

// currentOffset returns current word offset
func (c *Compiler) currentOffset() int {
	return len(c.currentScript.Code)
}

// buildCOB builds the final scripting.COB structure
func (c *Compiler) buildCOB() *scripting.COB {
	// Build script names first
	scriptNames := make([]string, len(c.scripts))
	for i, s := range c.scripts {
		scriptNames[i] = s.Name
	}

	// Build code section
	// First pass: calculate base offsets for each script
	code := []byte{}
	indices := []uint32{}

	for _, script := range c.scripts {
		baseOffset := uint32(len(code) / 4)
		indices = append(indices, baseOffset)

		// Adjust jump operands from script-local to absolute code-section offsets
		for i := 0; i < len(script.Code); i++ {
			opcode := script.Code[i]
			paramCount := scripting.OpcodeParamCount(opcode)
			if (opcode == scripting.OP_JUMP || opcode == scripting.OP_JUMP_IF_FALSE) && i+1 < len(script.Code) {
				// Operand is a script-local word offset — add base to make absolute
				script.Code[i+1] += baseOffset
			}
			i += paramCount // Skip operands
		}

		for _, word := range script.Code {
			buf := make([]byte, 4)
			binary.LittleEndian.PutUint32(buf, word)
			code = append(code, buf...)
		}
	}

	// Pick the version: explicit .version directive wins; otherwise
	// preserve TA's historical default of 4. TA: Kingdoms v6 .cob files
	// carry an 8-byte sub-header between the canonical 44-byte header and
	// the code section that the writer reconstructs from the structured
	// fields below.
	version := c.version()
	subHeaderSize := 0
	if version == kingdomsVersion {
		subHeaderSize = 8
	}

	codeOffset := 44 + subHeaderSize
	scriptCodeIndexOffset := codeOffset + len(code)
	scriptNameOffset := scriptCodeIndexOffset + len(c.scripts)*4
	pieceNameOffset := scriptNameOffset + len(c.scripts)*4
	soundNameOffset := pieceNameOffset + len(c.pieceNames)*4

	return &scripting.COB{
		VersionSignature:   uint32(version),
		NumScripts:         uint32(len(c.scripts)),
		NumPieces:          uint32(len(c.pieceNames)),
		LengthOfScripts:    uint32(len(code) / 4),
		NumberOfStaticVars: uint32(len(c.statics)),
		UKZero:             c.field5,
		// OffsetToNameArray == byte just past the piece-name array
		// (i.e. start of the sound-name offset table for v6, or
		// string-pool start for v4 where there is no sound-name table).
		OffsetToNameArray:             uint32(soundNameOffset),
		Code:                          code,
		ScriptCodeIndices:             indices,
		ScriptNames:                   scriptNames,
		PieceNames:                    c.pieceNames,
		SoundNames:                    c.soundNames,
		OffsetToScriptCode:            uint32(codeOffset),
		OffsetToScriptCodeIndexArray:  uint32(scriptCodeIndexOffset),
		OffsetToScriptNameOffsetArray: uint32(scriptNameOffset),
		OffsetToPieceNameOffsetArray:  uint32(pieceNameOffset),
	}
}

func validIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		letter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}
