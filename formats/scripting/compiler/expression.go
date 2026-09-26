package compiler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/coreprime/kbot-io/formats/scripting"
)

// Expressions are tokenised and compiled with C-like precedence, loosest
// first:
//
//	||  OR
//	XOR            (the game's second bitwise XOR, 0x10059000)
//	&&  AND
//	|
//	^              (bitwise XOR, 0x10037000)
//	&
//	==  !=
//	<  <=  >  >=
//	+  -
//	*  /
//
// with the prefix operators !, NOT, ~ (bitwise NOT) and unary minus binding
// tightest. All binary operators are left-associative.
//
// `%` is an error in TA (version 4) scripts: TA has no modulo instruction,
// and the word earlier versions emitted for it, 0x10037000, is TA's bitwise
// XOR. Under `.version 6` `%` still compiles to 0x10037000, with a warning,
// because what TA: Kingdoms does with that instruction is not established.
//
// Operands are integer literals, <degrees> and [distance] literals, locals,
// statics, piece names (their index), unit-value port names, TRUE/FALSE,
// parenthesised expressions and the value-producing calls:
//
//	get PORT                        GET_UNIT_VALUE
//	get PORT(a, ...)                GET with the port and up to four arguments
//	get(port, a, ...)               GET, same operands
//	rand(low, high)
//	__is_carrying_unit(unit)        IS_CARRYING_UNIT
//	__carrier_unit_id()             CARRIER_UNIT_ID
//	play-sound(id, volume)          TA: Kingdoms only
//	Mission-Command("name", ...)    TA: Kingdoms only
//	__tak_math_09/0a/0b(a, b)       TA: Kingdoms only
//
// GET always pops a port and four arguments, so missing arguments are
// pushed as 0, exactly as the retail compiler does for `get PORT(x)`.

// compileExpression compiles one expression, leaving its value on the stack.
func (c *Compiler) compileExpression(expr string) error {
	toks, err := tokenize(expr, c.angleUnits == angleUnitsRaw)
	if err != nil {
		return err
	}
	p := &exprParser{c: c, toks: toks}
	if err := p.parseExpr(0); err != nil {
		return fmt.Errorf("%w in %q", err, strings.TrimSpace(expr))
	}
	if t := p.peek(); t.kind != tokEOF {
		return fmt.Errorf("unexpected %q in %q", t.text, strings.TrimSpace(expr))
	}
	return nil
}

// compileOperand compiles an operand of a movement statement. Earlier
// decompilers wrapped every operand in angle brackets, including
// expressions such as <local_0>. When the operand starts with `<`, ends
// with `>` and the opening bracket does not begin a <number> literal, the
// brackets wrap the whole operand and are dropped; an operand such as
// <5> + <3>, made of literals, is compiled as written.
func (c *Compiler) compileOperand(text string) error {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") && strings.HasSuffix(text, ">") && !angleLiteralRE.MatchString(text) {
		return c.compileExpression(text[1 : len(text)-1])
	}
	return c.compileExpression(text)
}

type tokKind int

const (
	tokEOF tokKind = iota
	tokNumber
	tokIdent
	tokString
	tokOp
)

type token struct {
	kind tokKind
	text string
	val  int32 // tokNumber
	// spaced is set when white space precedes the token; `get(` and
	// `get (` are different forms.
	spaced bool
}

var (
	angleLiteralRE  = regexp.MustCompile(`^<\s*([-+]?\s*(?:\d+\.?\d*|\.\d+))\s*>`)
	linearLiteralRE = regexp.MustCompile(`^\[\s*([-+]?\s*(?:\d+\.?\d*|\.\d+))\s*\]`)
	numberRE        = regexp.MustCompile(`^(0[xX][0-9a-fA-F]+|\d+)`)
	identRE         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

// hyphenatedCalls are keywords whose name contains a hyphen.
var hyphenatedCalls = []string{"play-sound", "Mission-Command"}

var twoCharOps = []string{"||", "&&", "==", "!=", "<=", ">="}

// tokenize splits an expression into tokens. <n> and [n] literals become
// number tokens holding the converted integer.
func tokenize(s string, rawAngles bool) ([]token, error) {
	var toks []token
	spaced := false
	add := func(t token) {
		t.spaced = spaced
		spaced = false
		toks = append(toks, t)
	}
	for i := 0; i < len(s); {
		ch := s[i]
		rest := s[i:]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
			spaced = true
			i++
		case ch == '<' && angleLiteralRE.MatchString(rest):
			m := angleLiteralRE.FindStringSubmatch(rest)
			v, err := angleValue(m[1], rawAngles)
			if err != nil {
				return nil, err
			}
			add(token{kind: tokNumber, text: m[0], val: v})
			i += len(m[0])
		case ch == '[':
			m := linearLiteralRE.FindStringSubmatch(rest)
			if m == nil {
				return nil, fmt.Errorf("bad [distance] literal in %q", s)
			}
			v, err := scripting.ParseLinearLiteral(m[1])
			if err != nil {
				return nil, err
			}
			add(token{kind: tokNumber, text: m[0], val: v})
			i += len(m[0])
		case ch >= '0' && ch <= '9':
			m := numberRE.FindString(rest)
			v, err := parseIntLiteral(m)
			if err != nil {
				return nil, err
			}
			add(token{kind: tokNumber, text: m, val: v})
			i += len(m)
		case ch == '"' || ch == '`':
			end := stringLiteralEnd(rest)
			if end < 0 {
				return nil, fmt.Errorf("unterminated string in %q", s)
			}
			add(token{kind: tokString, text: rest[:end]})
			i += end
		case ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z'):
			word := identRE.FindString(rest)
			for _, kw := range hyphenatedCalls {
				if strings.HasPrefix(rest, kw) && (len(rest) == len(kw) || !isIdentByte(rest[len(kw)])) {
					word = kw
				}
			}
			add(token{kind: tokIdent, text: word})
			i += len(word)
		default:
			op := ""
			for _, two := range twoCharOps {
				if strings.HasPrefix(rest, two) {
					op = two
				}
			}
			if op == "" {
				if !strings.ContainsRune("+-*/%&|^~!<>(),", rune(ch)) {
					return nil, fmt.Errorf("unexpected character %q in %q", ch, s)
				}
				op = string(ch)
			}
			add(token{kind: tokOp, text: op})
			i += len(op)
		}
	}
	return toks, nil
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// stringLiteralEnd returns the length of the quoted string at the start of
// s, or -1 when it is not terminated.
func stringLiteralEnd(s string) int {
	quote := s[0]
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] == '\\' && quote == '"':
			i++
		case s[i] == quote:
			return i + 1
		}
	}
	return -1
}

func angleValue(text string, raw bool) (int32, error) {
	if !raw {
		return scripting.ParseAngleLiteral(text)
	}
	v, err := strconv.ParseInt(strings.ReplaceAll(text, " ", ""), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("bad <value> %q under `.angle_units raw`", text)
	}
	return int32(v), nil
}

// parseIntLiteral parses a decimal or hexadecimal literal of up to 32 bits.
// The value is the 32-bit word, so 4294967295 and 0xFFFFFFFF are -1 and
// -2147483648 (unary minus applied to 2147483648) is the smallest integer.
func parseIntLiteral(text string) (int32, error) {
	base, digits := 10, text
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
		base, digits = 16, text[2:]
	}
	v, err := strconv.ParseUint(digits, base, 32)
	if err != nil {
		return 0, fmt.Errorf("number %s does not fit in 32 bits", text)
	}
	return int32(uint32(v)), nil
}

type exprParser struct {
	c    *Compiler
	toks []token
	pos  int
}

func (p *exprParser) peek() token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token{kind: tokEOF}
}

func (p *exprParser) peekAt(n int) token {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n]
	}
	return token{kind: tokEOF}
}

func (p *exprParser) next() token {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *exprParser) isOp(text string) bool {
	t := p.peek()
	return t.kind == tokOp && t.text == text
}

func (p *exprParser) expectOp(text string) error {
	if !p.isOp(text) {
		t := p.peek()
		if t.kind == tokEOF {
			return fmt.Errorf("expected %q at the end", text)
		}
		return fmt.Errorf("expected %q, found %q", text, t.text)
	}
	p.next()
	return nil
}

type binaryOp struct {
	prec   int
	opcode uint32
}

var binaryOps = map[string]binaryOp{
	"||":  {1, scripting.OP_LOGICAL_OR},
	"OR":  {1, scripting.OP_LOGICAL_OR},
	"XOR": {2, scripting.OP_XOR_ALT},
	"&&":  {3, scripting.OP_LOGICAL_AND},
	"AND": {3, scripting.OP_LOGICAL_AND},
	"|":   {4, scripting.OP_BITWISE_OR},
	"^":   {5, scripting.OP_XOR},
	"&":   {6, scripting.OP_BITWISE_AND},
	"==":  {7, scripting.OP_EQUAL},
	"!=":  {7, scripting.OP_NOT_EQUAL},
	"<":   {8, scripting.OP_LESS_THAN},
	"<=":  {8, scripting.OP_LESS_OR_EQUAL},
	">":   {8, scripting.OP_GREATER_THAN},
	">=":  {8, scripting.OP_GREATER_EQUAL},
	"+":   {9, scripting.OP_ADD},
	"-":   {9, scripting.OP_SUB},
	"*":   {10, scripting.OP_MUL},
	"/":   {10, scripting.OP_DIV},
	"%":   {10, scripting.OP_XOR}, // version 6 only; see parseExpr
}

func (p *exprParser) peekBinary() (string, binaryOp, bool) {
	t := p.peek()
	if t.kind != tokOp && t.kind != tokIdent {
		return "", binaryOp{}, false
	}
	op, ok := binaryOps[t.text]
	if t.kind == tokIdent && t.text != "AND" && t.text != "OR" && t.text != "XOR" {
		return "", binaryOp{}, false
	}
	return t.text, op, ok
}

// parseExpr compiles operators binding at least as tightly as minPrec.
func (p *exprParser) parseExpr(minPrec int) error {
	if err := p.parseUnary(); err != nil {
		return err
	}
	for {
		text, op, ok := p.peekBinary()
		if !ok || op.prec < minPrec {
			return nil
		}
		p.next()
		if text == "%" {
			if err := p.c.modulo(); err != nil {
				return err
			}
		}
		if err := p.parseExpr(op.prec + 1); err != nil {
			return err
		}
		p.c.emit(op.opcode, 0)
	}
}

func (p *exprParser) parseUnary() error {
	t := p.peek()
	switch {
	case t.kind == tokOp && t.text == "!", t.kind == tokIdent && t.text == "NOT":
		p.next()
		if err := p.parseUnary(); err != nil {
			return err
		}
		p.c.emit(scripting.OP_LOGICAL_NOT, 0)
		return nil
	case t.kind == tokOp && t.text == "~":
		p.next()
		if err := p.parseUnary(); err != nil {
			return err
		}
		p.c.emit(scripting.OP_NOT, 0)
		return nil
	case t.kind == tokOp && t.text == "+":
		p.next()
		return p.parseUnary()
	case t.kind == tokOp && t.text == "-":
		p.next()
		if n := p.peek(); n.kind == tokNumber {
			p.next()
			p.c.emit(scripting.OP_PUSH_CONSTANT, int32(-int64(n.val)))
			return nil
		}
		// -x is 0 - x: the game has no negation instruction.
		p.c.emit(scripting.OP_PUSH_CONSTANT, 0)
		if err := p.parseUnary(); err != nil {
			return err
		}
		p.c.emit(scripting.OP_SUB, 0)
		return nil
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() error {
	t := p.next()
	switch t.kind {
	case tokEOF:
		return fmt.Errorf("missing operand")
	case tokNumber:
		p.c.emit(scripting.OP_PUSH_CONSTANT, t.val)
		return nil
	case tokString:
		return fmt.Errorf("string %s is only allowed as the first argument of Mission-Command", t.text)
	case tokOp:
		if t.text != "(" {
			return fmt.Errorf("unexpected %q", t.text)
		}
		if err := p.parseExpr(0); err != nil {
			return err
		}
		return p.expectOp(")")
	}

	switch t.text {
	case "get":
		return p.parseGet()
	case "rand":
		n, err := p.parseArgs()
		if err != nil {
			return err
		}
		if n != 2 {
			return fmt.Errorf("rand takes 2 arguments, got %d", n)
		}
		p.c.emit(scripting.OP_RAND, 0)
		return nil
	case "__is_carrying_unit":
		n, err := p.parseArgs()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("__is_carrying_unit takes 1 argument, got %d", n)
		}
		p.c.emit(scripting.OP_IS_CARRYING_UNIT, 0)
		return nil
	case "__carrier_unit_id":
		n, err := p.parseArgs()
		if err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("__carrier_unit_id takes no arguments, got %d", n)
		}
		p.c.emit(scripting.OP_CARRIER_UNIT_ID, 0)
		return nil
	case "__tak_math_09", "__tak_math_0a", "__tak_math_0b":
		if err := p.c.requireKingdoms(t.text); err != nil {
			return err
		}
		n, err := p.parseArgs()
		if err != nil {
			return err
		}
		// Each of these operators pops two values. (Earlier decompilers
		// wrote a one-argument form that dropped the first operand.)
		if n != 2 {
			return fmt.Errorf("%s takes 2 arguments, got %d", t.text, n)
		}
		p.c.emit(takMathOpcodes[t.text], 0)
		return nil
	case "play-sound":
		return p.parsePlaySound()
	case "Mission-Command":
		return p.parseMissionCommand()
	}

	if p.isOp("(") {
		if _, isScript := p.c.scriptIndex[t.text]; isScript {
			return fmt.Errorf("%s() is a function; call it with call-script or start-script", t.text)
		}
		return fmt.Errorf("unknown function %s()", t.text)
	}
	return p.c.emitIdentifier(t.text)
}

// modulo checks a `%` operator: an error in a TA script, and a warning
// (once per function) under `.version 6`, where it compiles to 0x10037000.
func (c *Compiler) modulo() error {
	if !c.kingdoms() {
		return fmt.Errorf("TA has no modulo instruction (0x10037000 is its bitwise XOR); "+
			"`%%` compiles only after `.version %d`", kingdomsVersion)
	}
	name := ""
	if c.currentScript != nil {
		name = c.currentScript.Name
	}
	msg := fmt.Sprintf("function %s: `%%` compiles to 0x10037000, which TA runs as bitwise XOR; "+
		"what TA: Kingdoms does with that instruction is not established", name)
	for _, w := range c.warnings {
		if w == msg {
			return nil
		}
	}
	c.warnings = append(c.warnings, msg)
	return nil
}

var takMathOpcodes = map[string]uint32{
	"__tak_math_09": scripting.OP_TAK_MATH_09,
	"__tak_math_0a": scripting.OP_TAK_MATH_0A,
	"__tak_math_0b": scripting.OP_TAK_MATH_0B,
}

// emitIdentifier pushes the value an identifier names.
func (c *Compiler) emitIdentifier(name string) error {
	if idx, ok := c.localIndex[name]; ok {
		c.emit(scripting.OP_PUSH_LOCAL_VAR, int32(idx))
		return nil
	}
	if idx, ok := c.staticIndex[name]; ok {
		c.emit(scripting.OP_PUSH_STATIC, int32(idx))
		return nil
	}
	if idx, ok := c.pieces[name]; ok {
		c.emit(scripting.OP_PUSH_CONSTANT, int32(idx))
		return nil
	}
	if port := c.getPortNumber(name); port >= 0 {
		c.emit(scripting.OP_PUSH_CONSTANT, int32(port))
		return nil
	}
	switch strings.ToUpper(name) {
	case "TRUE":
		c.emit(scripting.OP_PUSH_CONSTANT, 1)
		return nil
	case "FALSE":
		c.emit(scripting.OP_PUSH_CONSTANT, 0)
		return nil
	}
	if strings.HasPrefix(name, "piece_") {
		if idx, err := c.getPieceIndex(name); err == nil {
			c.emit(scripting.OP_PUSH_CONSTANT, int32(idx))
			return nil
		}
	}
	return fmt.Errorf("unknown identifier %s", name)
}

// parseArgs compiles a parenthesised, comma-separated argument list and
// returns the number of arguments.
func (p *exprParser) parseArgs() (int, error) {
	if err := p.expectOp("("); err != nil {
		return 0, err
	}
	if p.isOp(")") {
		p.next()
		return 0, nil
	}
	n := 0
	for {
		if err := p.parseExpr(0); err != nil {
			return n, err
		}
		n++
		if p.isOp(",") {
			p.next()
			continue
		}
		return n, p.expectOp(")")
	}
}

// getOperands is the number of values GET pops: the port and four
// arguments.
const getOperands = 5

// parseGet compiles the three forms after the `get` keyword.
func (p *exprParser) parseGet() error {
	pad := func(n int) error {
		if n == 0 {
			return fmt.Errorf("get() needs at least the port")
		}
		if n > getOperands {
			return fmt.Errorf("get takes a port and at most %d arguments, got %d values", getOperands-1, n)
		}
		for ; n < getOperands; n++ {
			p.c.emit(scripting.OP_PUSH_CONSTANT, 0)
		}
		p.c.emit(scripting.OP_GET, 0)
		return nil
	}

	// get(port, args...), written without a space; `get (x)` is get PORT.
	if p.isOp("(") && !p.peek().spaced {
		n, err := p.parseArgs()
		if err != nil {
			return err
		}
		return pad(n)
	}

	// get PORT(args...)
	t, after := p.peek(), p.peekAt(1)
	if after.kind == tokOp && after.text == "(" && !after.spaced &&
		(t.kind == tokNumber || (t.kind == tokIdent && !isCallKeyword(t.text))) {
		p.next()
		if t.kind == tokNumber {
			p.c.emit(scripting.OP_PUSH_CONSTANT, t.val)
		} else if err := p.c.emitIdentifier(t.text); err != nil {
			return err
		}
		n, err := p.parseArgs()
		if err != nil {
			return err
		}
		return pad(n + 1)
	}

	// get PORT
	if err := p.parseUnary(); err != nil {
		return err
	}
	p.c.emit(scripting.OP_GET_UNIT_VALUE, 0)
	return nil
}

func isCallKeyword(name string) bool {
	switch name {
	case "get", "rand", "play-sound", "Mission-Command", "__is_carrying_unit", "__carrier_unit_id",
		"__tak_math_09", "__tak_math_0a", "__tak_math_0b":
		return true
	}
	return false
}

// parsePlaySound compiles play-sound(id, volume): the id is pushed and the
// volume, a constant, is the inline operand.
func (p *exprParser) parsePlaySound() error {
	if err := p.c.requireKingdoms("play-sound"); err != nil {
		return err
	}
	if err := p.expectOp("("); err != nil {
		return err
	}
	if err := p.parseExpr(0); err != nil {
		return err
	}
	if err := p.expectOp(","); err != nil {
		return err
	}
	neg := false
	if p.isOp("-") {
		p.next()
		neg = true
	}
	vol := p.next()
	if vol.kind != tokNumber {
		return fmt.Errorf("play-sound volume must be a number")
	}
	if err := p.expectOp(")"); err != nil {
		return err
	}
	v := vol.val
	if neg {
		v = int32(-int64(v))
	}
	p.c.emit(scripting.OP_PLAY_SOUND, v)
	return nil
}

// parseMissionCommand compiles Mission-Command(name, args...): the
// arguments are pushed and the sound-name index and argument count are
// inline.
func (p *exprParser) parseMissionCommand() error {
	if err := p.c.requireKingdoms("Mission-Command"); err != nil {
		return err
	}
	if err := p.expectOp("("); err != nil {
		return err
	}
	name := p.next()
	var cmdIdx int
	var err error
	switch name.kind {
	case tokString, tokIdent:
		cmdIdx, err = p.c.takSoundNameIndex(name.text)
	default:
		err = fmt.Errorf("Mission-Command needs a sound name first")
	}
	if err != nil {
		return err
	}
	argc := 0
	for p.isOp(",") {
		p.next()
		if err := p.parseExpr(0); err != nil {
			return err
		}
		argc++
	}
	if err := p.expectOp(")"); err != nil {
		return err
	}
	p.c.emit2(scripting.OP_MISSION_COMMAND, int32(cmdIdx), int32(argc))
	return nil
}

// getPortNumber returns the port number for a port name. Ports 1–20 are
// the standard TA set; ports 21+ are TA: Kingdoms additions taken from
// Scriptor's [UNITVLAUES] table.
func (c *Compiler) getPortNumber(name string) int {
	if num, ok := unitValuePorts[name]; ok {
		return num
	}
	return -1
}

var unitValuePorts = map[string]int{
	"ACTIVATION":         1,
	"STANDINGMOVEORDERS": 2,
	"STANDINGFIREORDERS": 3,
	"HEALTH":             4,
	"INBUILDSTANCE":      5,
	"BUSY":               6,
	"PIECE_XZ":           7,
	"PIECE_Y":            8,
	"UNIT_XZ":            9,
	"UNIT_Y":             10,
	"UNIT_HEIGHT":        11,
	"XZ_ATAN":            12,
	"XZ_HYPOT":           13,
	"ATAN":               14,
	"HYPOT":              15,
	"GROUND_HEIGHT":      16,
	"BUILD_PERCENT_LEFT": 17,
	"YARD_OPEN":          18,
	"BUGGER_OFF":         19,
	"ARMORED":            20,
	// TA: Kingdoms additions.
	"WEAPON_AIM_ABORTED": 21,
	"WEAPON_READY":       22,
	"WEAPON_LAUNCH_NOW":  23,
	"FINISHED_DYING":     26,
	"ORIENTATION":        27,
	"IN_WATER":           28,
	"CURRENT_SPEED":      29,
	"MAGIC_DEATH":        31,
	"VETERAN_LEVEL":      32,
	"ON_ROAD":            34,
}

// parseScriptCall splits "ScriptName(param1, param2)" into name and param string.
func parseScriptCall(s string) (name, params string) {
	idx := strings.IndexByte(s, '(')
	if idx < 0 {
		return strings.TrimSpace(s), ""
	}
	name = strings.TrimSpace(s[:idx])
	// Find matching close paren from the end
	inner := strings.TrimSpace(s[idx+1:])
	if len(inner) > 0 && inner[len(inner)-1] == ')' {
		inner = inner[:len(inner)-1]
	}
	return name, strings.TrimSpace(inner)
}

// splitParams splits a comma-separated parameter list respecting parenthesis
// nesting and double-quoted string literals. Quoted strings can themselves
// contain commas — e.g. TA: Kingdoms `Mission-Command("SetMission o 1, s", …)`
// — so the parameter splitter must not break on commas inside a quoted span.
func splitParams(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var parts []string
	depth := 0
	inString := false
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inString:
			if ch == '\\' && i+1 < len(s) {
				i++ // skip the escaped byte
				continue
			}
			if ch == '"' {
				inString = false
			}
		case ch == '"':
			inString = true
		case ch == '(':
			depth++
		case ch == ')':
			depth--
		case ch == ',' && depth == 0:
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}
