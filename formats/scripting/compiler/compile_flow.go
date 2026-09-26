package compiler

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/coreprime/kbot-io/formats/scripting"
)

func (c *Compiler) compileBlock(lines []string) error {
	i := 0
	for i < len(lines) {
		line := stripLineComment(strings.TrimSpace(lines[i]))

		if line == "" {
			i++
			continue
		}

		// Check statement type
		var err error
		switch {
		case hasKeyword(line, "return"):
			err = c.compileReturn(line)

		case hasKeyword(line, "sleep"):
			err = c.compileSleep(line)

		case strings.HasPrefix(line, "set "):
			err = c.compileSet(line)

		case strings.HasPrefix(line, "move "):
			err = c.compileMove(line)

		case strings.HasPrefix(line, "turn "):
			err = c.compileTurn(line)

		case strings.HasPrefix(line, "spin "):
			err = c.compileSpin(line)

		case strings.HasPrefix(line, "stop-spin "):
			err = c.compileStopSpin(line)

		case strings.HasPrefix(line, "hide "):
			err = c.compileHide(line)

		case strings.HasPrefix(line, "show "):
			err = c.compileShow(line)

		case strings.HasPrefix(line, "explode "):
			err = c.compileExplode(line)

		case strings.HasPrefix(line, "emit-sfx "):
			err = c.compileEmitSfx(line)

		case strings.HasPrefix(line, "cache "):
			err = c.compileCache(line)

		case strings.HasPrefix(line, "dont-cache "):
			err = c.compileDontCache(line)

		case strings.HasPrefix(line, "shade "):
			err = c.compileShade(line)

		case strings.HasPrefix(line, "dont-shade "):
			err = c.compileDontShade(line)

		case hasKeyword(line, "signal"):
			err = c.compileSignal(line)

		case hasKeyword(line, "set-signal-mask"):
			err = c.compileSetSignalMask(line)

		case strings.HasPrefix(line, "start-script "):
			err = c.compileStartScript(line)

		case strings.HasPrefix(line, "call-script "):
			err = c.compileCallScript(line)

		case strings.HasPrefix(line, "wait-for-turn "):
			err = c.compileWaitForTurn(line)

		case strings.HasPrefix(line, "attach-unit "):
			err = c.compileAttachUnit(line)

		case strings.HasPrefix(line, "drop-unit "):
			err = c.compileDropUnit(line)

		case strings.HasPrefix(line, "dont-shadow("):
			err = c.compileDontShadow(line)

		case strings.HasPrefix(line, "Mission-Command("):
			err = c.compileMissionCommandStatement(line)

		case strings.HasPrefix(line, "play-sound("):
			// Compile as the value-producing expression then drop the
			// return value (POP_STACK) — matches the
			// `PUSH id ; PLAY_SOUND vol ; POP_STACK` shape Cavedog emits
			// for stand-alone calls.
			if err = c.compileExpression(strings.TrimSuffix(line, ";")); err == nil {
				c.emit(scripting.OP_POP_STACK, 0)
			}

		case strings.HasPrefix(line, "__discard_call("):
			err = c.compileDiscardCall(line)

		case strings.HasPrefix(line, "__piece_op_09("):
			err = c.compilePieceOp09(line)

		case strings.HasPrefix(line, "wait-for-move "):
			err = c.compileWaitForMove(line)

		case hasKeyword(line, "while"):
			consumed, err := c.compileWhile(lines[i:])
			if err != nil {
				return err
			}
			i += consumed
			continue

		case hasKeyword(line, "if"):
			consumed, err := c.compileIf(lines[i:])
			if err != nil {
				return err
			}
			i += consumed
			continue

		case containsAssignment(line):
			err = c.compileAssignment(line)

		default:
			return fmt.Errorf("unknown statement: %s", line)
		}
		if err != nil {
			return err
		}

		i++
	}

	return nil
}

// hasKeyword reports whether line starts with the keyword followed by the
// end of the line, white space, '(' or ';'.
func hasKeyword(line, keyword string) bool {
	if !strings.HasPrefix(line, keyword) {
		return false
	}
	if len(line) == len(keyword) {
		return true
	}
	switch line[len(keyword)] {
	case ' ', '\t', '(', ';':
		return true
	}
	return false
}

// stripLineComment removes a trailing // comment outside string literals.
func stripLineComment(line string) string {
	inString := false
	for i := 0; i+1 < len(line); i++ {
		switch {
		case inString && line[i] == '\\':
			i++
		case line[i] == '"':
			inString = !inString
		case !inString && line[i] == '/' && line[i+1] == '/':
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

func (c *Compiler) compileReturn(line string) error {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")

	valStr := strings.TrimSpace(strings.TrimPrefix(line, "return"))
	if valStr == "" {
		// `return;` (no expression) lays down a STACK_ALLOC prefix for
		// TA: Kingdoms v6 .cob output — every retail TAK function ends
		// with the pattern `… ; STACK_ALLOC ; RETURN` for a value-less
		// return. TA's v4 compiler doesn't emit anything extra.
		if c.kingdoms() {
			c.emit(scripting.OP_STACK_ALLOC, 0)
		}
		c.emit(scripting.OP_RETURN, 0)
		return nil
	}

	// `return <expr>;` compiles the expression and then RETURN. Empirically
	// the TAK compiler emits no STACK_ALLOC prefix on value-returning
	// returns (e.g. `return 0;` → `PUSH_CONST 0 ; RETURN`).
	if err := c.compileExpression(valStr); err != nil {
		return err
	}
	c.emit(scripting.OP_RETURN, 0)
	return nil
}

func (c *Compiler) compileWhile(lines []string) (int, error) {
	// Extract condition
	firstLine := strings.TrimSpace(lines[0])
	condRE := regexp.MustCompile(`^while\s*\((.+)\)\s*$`)
	m := condRE.FindStringSubmatch(firstLine)
	if m == nil {
		return 0, fmt.Errorf("invalid while syntax: %s", firstLine)
	}
	condition := m[1]

	// Find body (between { and })
	if len(lines) < 2 || strings.TrimSpace(lines[1]) != "{" {
		return 0, fmt.Errorf("expected '{' after while")
	}

	depth := 1
	end := 2
	for end < len(lines) && depth > 0 {
		l := strings.TrimSpace(lines[end])
		switch l {
		case "{":
			depth++
		case "}":
			depth--
		}
		end++
	}

	bodyLines := lines[2 : end-1]

	// Compile: loop_start: condition, JUMP_IF_FALSE exit, body, JUMP loop_start
	loopStart := c.currentOffset()

	if err := c.compileExpression(condition); err != nil {
		return 0, err
	}
	jumpExit := c.emitPlaceholder(scripting.OP_JUMP_IF_FALSE)

	if err := c.compileBlock(bodyLines); err != nil {
		return 0, err
	}

	c.emit(scripting.OP_JUMP, int32(loopStart))
	c.patchJump(jumpExit, c.currentOffset())

	return end, nil
}

// compileIf compiles if statement
func (c *Compiler) compileIf(lines []string) (int, error) {
	// Extract condition
	firstLine := strings.TrimSpace(lines[0])
	condRE := regexp.MustCompile(`^if\s*\((.+)\)\s*$`)
	m := condRE.FindStringSubmatch(firstLine)
	if m == nil {
		return 0, fmt.Errorf("invalid if syntax: %s", firstLine)
	}
	condition := m[1]

	// Find then block
	if len(lines) < 2 || strings.TrimSpace(lines[1]) != "{" {
		return 0, fmt.Errorf("expected '{' after if")
	}

	depth := 1
	end := 2
	for end < len(lines) && depth > 0 {
		l := strings.TrimSpace(lines[end])
		switch l {
		case "{":
			depth++
		case "}":
			depth--
		}
		end++
	}

	thenLines := lines[2 : end-1]

	// Compile: condition, JUMP_IF_FALSE else/end, then_body, (JUMP end), else_body
	if err := c.compileExpression(condition); err != nil {
		return 0, err
	}
	jumpElse := c.emitPlaceholder(scripting.OP_JUMP_IF_FALSE)

	if err := c.compileBlock(thenLines); err != nil {
		return 0, err
	}

	// Check for else
	if end < len(lines) && strings.TrimSpace(lines[end]) == "else" {
		jumpEnd := c.emitPlaceholder(scripting.OP_JUMP)
		c.patchJump(jumpElse, c.currentOffset())

		// Find else body
		if end+1 >= len(lines) || strings.TrimSpace(lines[end+1]) != "{" {
			return 0, fmt.Errorf("expected '{' after else")
		}

		depth = 1
		elseStart := end + 2
		elseEnd := elseStart
		for elseEnd < len(lines) && depth > 0 {
			l := strings.TrimSpace(lines[elseEnd])
			switch l {
			case "{":
				depth++
			case "}":
				depth--
			}
			elseEnd++
		}

		elseLines := lines[elseStart : elseEnd-1]
		if err := c.compileBlock(elseLines); err != nil {
			return 0, err
		}
		c.patchJump(jumpEnd, c.currentOffset())

		return elseEnd, nil
	}

	c.patchJump(jumpElse, c.currentOffset())
	return end, nil
}
