package compiler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/coreprime/kbot-io/formats/scripting"
)

// Statement intrinsics for TA instructions that have no BOS keyword. The
// decompiler writes them so that every instruction the game runs has a BOS
// form:
//
//	__discard_call(<word>, <args>...);   DISCARD_CALL: pushes the arguments,
//	                                     which the game pops and discards
//	__piece_op_09(<piece>, <a>, <b>);    PIECE_OP_09 with its two stack values

// maxDiscardCallArgs is the size of the buffer the game copies the
// arguments of DISCARD_CALL into.
const maxDiscardCallArgs = 4

// compileDiscardCall handles "__discard_call(<word>, <args>...);".
func (c *Compiler) compileDiscardCall(line string) error {
	args, err := stripCallTAK(line, "__discard_call")
	if err != nil {
		return err
	}
	parts := splitParams(args)
	if len(parts) < 1 {
		return fmt.Errorf("__discard_call needs its first inline word")
	}
	word, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 0, 32)
	if err != nil {
		return fmt.Errorf("__discard_call inline word must be an integer: %q", parts[0])
	}
	stackArgs := parts[1:]
	if len(stackArgs) > maxDiscardCallArgs {
		return fmt.Errorf("__discard_call with %d arguments overruns the game's %d-word buffer",
			len(stackArgs), maxDiscardCallArgs)
	}
	for _, a := range stackArgs {
		if err := c.compileExpression(a); err != nil {
			return err
		}
	}
	c.emit2(scripting.OP_DISCARD_CALL, int32(word), int32(len(stackArgs)))
	return nil
}

// compilePieceOp09 handles "__piece_op_09(<piece>, <a>, <b>);".
func (c *Compiler) compilePieceOp09(line string) error {
	args, err := stripCallTAK(line, "__piece_op_09")
	if err != nil {
		return err
	}
	parts := splitParams(args)
	if len(parts) != 3 {
		return fmt.Errorf("__piece_op_09 expects a piece and two values, got %d arguments", len(parts))
	}
	pieceIdx, err := c.getPieceIndex(strings.TrimSpace(parts[0]))
	if err != nil {
		return err
	}
	for _, a := range parts[1:] {
		if err := c.compileExpression(a); err != nil {
			return err
		}
	}
	c.emit(scripting.OP_PIECE_OP_09, int32(pieceIdx))
	return nil
}
