package decompiler

import (
	"fmt"
	"strings"

	scripting "github.com/coreprime/kbot-io/formats/scripting"
)

// Block represents a control flow block (if, while, or sequence of statements)
type Block struct {
	Type       string   // "if", "while", "sequence"
	Condition  string   // For if/while
	ThenBlock  *Block   // For if/while
	ElseBlock  *Block   // For if (optional)
	Statements []string // For sequence
	Children   []*Block // For nested blocks
}

// ControlFlowAnalyzer recursively processes instructions to build control flow structure
type ControlFlowAnalyzer struct {
	instructions []scripting.Instruction
	stack        *exprStack
	paramNames   map[int]string
	signalDef    map[int]string
	globalNames  map[int]string
	decompiler   *Decompiler
}

// NewControlFlowAnalyzer creates a new analyzer
func NewControlFlowAnalyzer(decompiler *Decompiler, instructions []scripting.Instruction, paramNames map[int]string, signalDef map[int]string, globalNames map[int]string) *ControlFlowAnalyzer {
	return &ControlFlowAnalyzer{
		instructions: instructions,
		stack:        newExprStack(),
		paramNames:   paramNames,
		signalDef:    signalDef,
		globalNames:  globalNames,
		decompiler:   decompiler,
	}
}

// ProcessRange processes a range of instructions [start, end) and returns
// the BOS lines for it.
//
// Jumps are only ever printed as the if, else and while blocks the compiler
// turns back into the same jumps:
//
//	if:     cond; JUMP_IF_FALSE end; then...; end:
//	else:   cond; JUMP_IF_FALSE else; then...; JUMP end; else: ...; end:
//	while:  top: cond; JUMP_IF_FALSE end; body...; JUMP top; end:
//
// Any other jump (a forward JUMP that is not the end of a then-block, a
// backward JUMP that is not the end of a loop, or a target outside the
// enclosing block) cannot be written in BOS; the decompiler reports it
// instead of dropping it.
func (cfa *ControlFlowAnalyzer) ProcessRange(start, end int, indent int) []string {
	var output []string
	indentStr := strings.Repeat("\t", indent)
	exprStart := start // first instruction of the expression being built
	i := start

	for i < end {
		if cfa.stack.isEmpty() {
			exprStart = i
		}
		// Check for RETURN
		if cfa.instructions[i].Opcode == scripting.OP_RETURN {
			// Process return value if on stack
			if !cfa.stack.isEmpty() {
				retval := cfa.stack.pop()
				output = append(output, fmt.Sprintf("%sreturn %s;", indentStr, retval))
			} else {
				output = append(output, indentStr+"return;")
			}
			i++
			continue
		}
		// Check for control flow patterns
		if i+1 < end && cfa.instructions[i+1].Opcode == scripting.OP_JUMP_IF_FALSE {
			block, nextI := cfa.processConditional(exprStart, i, end, indent)
			output = append(output, block...)
			i = nextI
			continue
		}

		// Regular statement
		stmt := cfa.decompiler.translateInstruction(cfa.instructions[i], cfa.stack, cfa.paramNames, cfa.signalDef, cfa.globalNames)
		if stmt != "" {
			output = append(output, indentStr+stmt)
		}
		i++
	}

	return output
}

// processConditional processes the if, if/else or while statement whose
// condition ends with instruction i (condStart is the first instruction of
// the condition) and whose JUMP_IF_FALSE is instruction i+1. It returns the
// decompiled lines and the next instruction index to process. When the
// jumps do not form a block, it records the error and skips to end.
func (cfa *ControlFlowAnalyzer) processConditional(condStart, i, end int, indent int) ([]string, int) {
	d := cfa.decompiler
	condInst := cfa.instructions[i]
	jif := cfa.instructions[i+1]

	stmt := d.translateInstruction(condInst, cfa.stack, cfa.paramNames, cfa.signalDef, cfa.globalNames)
	if stmt != "" || cfa.stack.isEmpty() {
		d.fail("JUMP_IF_FALSE at 0x%04X does not follow a condition; use the disassembler", jif.Offset)
		return nil, end
	}
	condition := cleanParentheses(cfa.stack.pop())

	bodyStart := i + 2
	exit, ok := cfa.jumpTarget(jif, bodyStart, end)
	if !ok {
		d.fail("JUMP_IF_FALSE at 0x%04X to 0x%04X does not end an if or while block; use the disassembler",
			jif.Offset, targetOffset(jif))
		return nil, end
	}

	indentStr := strings.Repeat("\t", indent)
	block := func(keyword string, from, to int) []string {
		lines := []string{indentStr + keyword, indentStr + "{"}
		lines = append(lines, cfa.ProcessRange(from, to, indent+1)...)
		return append(lines, indentStr+"}")
	}

	if exit > bodyStart {
		last := cfa.instructions[exit-1]
		if last.Opcode == scripting.OP_JUMP && targetOffset(last) <= uint64(condInst.Offset) {
			// A backward JUMP ending the body: a while loop, which must
			// jump back to the first instruction of its condition.
			if targetOffset(last) != uint64(cfa.instructions[condStart].Offset) {
				d.fail("JUMP at 0x%04X to 0x%04X does not return to the start of the loop condition; use the disassembler",
					last.Offset, targetOffset(last))
				return nil, end
			}
			return block(fmt.Sprintf("while (%s)", condition), bodyStart, exit-1), exit
		}
		if last.Opcode == scripting.OP_JUMP && targetOffset(last) > uint64(last.Offset) {
			// A forward JUMP ending the then-block skips the else-block.
			// One that goes straight to exit may instead end an inner
			// if/else with an empty else-block, as in
			// `if (a) { if (b) { x; } else { } }`; try that reading first.
			if targetOffset(last) == cfa.offsetAt(exit) && d.err == nil {
				saved := cfa.stack.snapshot()
				if lines := block(fmt.Sprintf("if (%s)", condition), bodyStart, exit); d.err == nil {
					return lines, exit
				}
				cfa.stack.restore(saved)
				d.err = nil
			}
			elseEnd, ok := cfa.jumpTarget(last, exit, end)
			if !ok {
				d.fail("JUMP at 0x%04X to 0x%04X does not end an else block; use the disassembler",
					last.Offset, targetOffset(last))
				return nil, end
			}
			output := block(fmt.Sprintf("if (%s)", condition), bodyStart, exit-1)
			output = append(output, block("else", exit, elseEnd)...)
			return output, elseEnd
		}
	}
	return block(fmt.Sprintf("if (%s)", condition), bodyStart, exit), exit
}

// targetOffset returns the code offset a jump goes to.
func targetOffset(inst scripting.Instruction) uint64 {
	return uint64(uint32(inst.Operand)) * 4
}

// offsetAt returns the code offset of instruction idx, or the offset just
// past the last instruction when idx is len(instructions).
func (cfa *ControlFlowAnalyzer) offsetAt(idx int) uint64 {
	if idx < len(cfa.instructions) {
		return uint64(cfa.instructions[idx].Offset)
	}
	last := cfa.instructions[len(cfa.instructions)-1]
	return uint64(last.Offset) + 4*uint64(1+scripting.OpcodeParamCount(last.Word()))
}

// jumpTarget returns the index in [lo, hi] of the instruction a jump goes
// to (hi meaning the end of the block), and false when the target is not an
// instruction boundary in that range.
func (cfa *ControlFlowAnalyzer) jumpTarget(jump scripting.Instruction, lo, hi int) (int, bool) {
	target := targetOffset(jump)
	for j := lo; j <= hi; j++ {
		off := cfa.offsetAt(j)
		if off == target {
			return j, true
		}
		if off > target {
			break
		}
	}
	return 0, false
}

// cleanParentheses removes redundant outer parentheses
func cleanParentheses(condition string) string {
	if len(condition) > 2 && condition[0] == '(' && condition[len(condition)-1] == ')' {
		inner := condition[1 : len(condition)-1]
		if !strings.Contains(inner, "&&") && !strings.Contains(inner, "||") {
			return inner
		}
	}
	return condition
}
