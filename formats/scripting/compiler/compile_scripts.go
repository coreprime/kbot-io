package compiler

import (
	"fmt"
	"strings"

	"github.com/coreprime/kbot-io/formats/scripting"
)

func (c *Compiler) compileStartScript(line string) error {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")

	// Parse: start-script ScriptName or start-script ScriptName(params...)
	rest := strings.TrimPrefix(line, "start-script ")
	scriptName, paramsStr := parseScriptCall(rest)

	// Look up script index
	scriptIdx, ok := c.scriptIndex[scriptName]
	if !ok {
		return fmt.Errorf("unknown script: %s", scriptName)
	}

	paramCount, err := c.compileArguments(paramsStr)
	if err != nil {
		return err
	}

	// Emit START_SCRIPT with script# and param_count
	c.emit2(scripting.OP_START_SCRIPT, int32(scriptIdx), int32(paramCount))
	return nil
}

// compileCallScript compiles call-script statement: call-script ScriptName[(param)];
func (c *Compiler) compileCallScript(line string) error {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")

	// Parse: call-script ScriptName or call-script ScriptName(params...)
	rest := strings.TrimPrefix(line, "call-script ")
	scriptName, paramsStr := parseScriptCall(rest)

	// Look up script index
	scriptIdx, ok := c.scriptIndex[scriptName]
	if !ok {
		return fmt.Errorf("unknown script: %s", scriptName)
	}

	paramCount, err := c.compileArguments(paramsStr)
	if err != nil {
		return err
	}

	// Emit CALL_SCRIPT with script# and param_count
	c.emit2(scripting.OP_CALL_SCRIPT, int32(scriptIdx), int32(paramCount))
	return nil
}

// compileArguments compiles a call's comma-separated argument list and
// returns the number of arguments.
func (c *Compiler) compileArguments(paramsStr string) (int, error) {
	count := 0
	for _, p := range splitParams(paramsStr) {
		if p == "" {
			return count, fmt.Errorf("empty argument in (%s)", paramsStr)
		}
		if err := c.compileExpression(p); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// compileAttachUnit compiles: attach-unit <uid> to <piece> <flag>;
func (c *Compiler) compileAttachUnit(line string) error {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")
	// Split on " to " to get uid and rest
	parts := strings.SplitN(strings.TrimPrefix(line, "attach-unit "), " to ", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid attach-unit syntax: %s", line)
	}
	uid := strings.TrimSpace(parts[0])
	rest := strings.TrimSpace(parts[1])
	// The last space-separated token is the flag; everything before it is the piece expression
	lastSpace := strings.LastIndex(rest, " ")
	if lastSpace < 0 {
		return fmt.Errorf("invalid attach-unit syntax: %s", line)
	}
	piece := strings.TrimSpace(rest[:lastSpace])
	flag := strings.TrimSpace(rest[lastSpace+1:])

	for _, operand := range []string{uid, piece, flag} {
		if err := c.compileExpression(operand); err != nil {
			return err
		}
	}
	c.emit(scripting.OP_ATTACH_UNIT, 0)
	return nil
}

// compileDropUnit compiles: drop-unit <uid>;
func (c *Compiler) compileDropUnit(line string) error {
	line = strings.TrimSuffix(strings.TrimSpace(line), ";")
	expr := strings.TrimPrefix(line, "drop-unit ")
	if err := c.compileExpression(expr); err != nil {
		return err
	}
	c.emit(scripting.OP_DROP_UNIT, 0)
	return nil
}
