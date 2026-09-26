package linter

import (
	"fmt"

	"github.com/coreprime/kbot-io/formats/scripting"
)

// kingdomsVersion is the COB version signature of TA: Kingdoms scripts.
const kingdomsVersion = 6

// TACompatRules returns the rules that check a COB against what TA 3.1c
// executes. They are keyed on the version signature: a COB that declares
// the TA: Kingdoms version (6) is skipped, every other COB is checked as a
// TA script. All findings are errors: the game faults, or corrupts the
// script's stack, when it reaches the instruction.
func TACompatRules() []Rule {
	return []Rule{
		&KingdomsOpcodeRule{},
		&UnknownOpcodeRule{},
		&PushPopFlagRule{},
		&StackLimitRule{},
		&GetArgumentsRule{},
		&StackUnderflowRule{},
		&DiscardCallRule{},
	}
}

// checksTA reports whether the TA rules apply to the file.
func checksTA(info *FileInfo) bool {
	return info.COB.VersionSignature != kingdomsVersion
}

// taDiagnostic builds an error diagnostic for one instruction.
func taDiagnostic(rule string, info *FileInfo, si ScriptInfo, format string, args ...any) Diagnostic {
	return Diagnostic{
		Rule:     rule,
		Severity: Error,
		Script:   si.Name,
		Line:     info.ScriptStartLines[si.Name],
		Message:  fmt.Sprintf(format, args...),
	}
}

// ── KingdomsOpcodeRule ─────────────────────────────────────────────────────

// KingdomsOpcodeRule reports TA: Kingdoms instructions (PLAY_SOUND,
// MISSION_COMMAND and the TAK_MATH operators) in a TA script. TA has no
// handler for them and the script faults when it reaches one.
type KingdomsOpcodeRule struct{}

func (r *KingdomsOpcodeRule) Name() string { return "ta-kingdoms-opcode" }

func (r *KingdomsOpcodeRule) Check(info *FileInfo) []Diagnostic {
	if !checksTA(info) {
		return nil
	}
	var diags []Diagnostic
	for _, si := range info.Scripts {
		for _, inst := range si.Instructions {
			op, ok := scripting.LookupOpcode(inst.Word())
			if !ok || !op.Kingdoms {
				continue
			}
			msg := "%s (0x%08X) at 0x%04X is a TA: Kingdoms instruction; TA faults when the script reaches it"
			if inst.Opcode == scripting.OP_TAK_MATH_0A {
				msg = "%s (0x%08X) at 0x%04X is not a TA instruction (some opcode tables call it NOT; " +
					"TA's bitwise NOT is 0x10038000); TA faults when the script reaches it"
			}
			diags = append(diags, taDiagnostic(r.Name(), info, si, msg, op.Name, inst.Word(), inst.Offset))
		}
	}
	return diags
}

// ── UnknownOpcodeRule ──────────────────────────────────────────────────────

// UnknownOpcodeRule reports opcode words that no game executes.
type UnknownOpcodeRule struct{}

func (r *UnknownOpcodeRule) Name() string { return "ta-unknown-opcode" }

func (r *UnknownOpcodeRule) Check(info *FileInfo) []Diagnostic {
	if !checksTA(info) {
		return nil
	}
	var diags []Diagnostic
	for _, si := range info.Scripts {
		for _, inst := range si.Instructions {
			if isPushOrPop(inst) {
				continue // PushPopFlagRule
			}
			if _, ok := scripting.LookupOpcode(inst.Word()); !ok {
				diags = append(diags, taDiagnostic(r.Name(), info, si,
					"opcode word 0x%08X at 0x%04X is not an instruction the game runs; the script faults there",
					inst.Word(), inst.Offset))
			}
		}
	}
	return diags
}

// ── PushPopFlagRule ────────────────────────────────────────────────────────

// PushPopFlagRule reports PUSH and POP words whose low three bits are not a
// source or destination the game accepts: PUSH takes 1 (constant), 2
// (local) or 4 (static), POP takes 2 or 4. Earlier tools wrote such words as
// PUSH_IMMEDIATE (0x10021000) and CREATE_LOCAL (0x10021008).
type PushPopFlagRule struct{}

func (r *PushPopFlagRule) Name() string { return "ta-push-flags" }

func (r *PushPopFlagRule) Check(info *FileInfo) []Diagnostic {
	if !checksTA(info) {
		return nil
	}
	var diags []Diagnostic
	for _, si := range info.Scripts {
		for _, inst := range si.Instructions {
			if !isPushOrPop(inst) {
				continue
			}
			word := inst.Word()
			flag := word & 7
			push := word&scripting.OpcodeDispatchMask == scripting.OP_PUSH_CONSTANT&scripting.OpcodeDispatchMask
			valid := flag == 2 || flag == 4 || (push && flag == 1)
			if valid {
				continue
			}
			kind := "POP"
			if push {
				kind = "PUSH"
			}
			diags = append(diags, taDiagnostic(r.Name(), info, si,
				"%s word 0x%08X at 0x%04X has flag %d; the game faults on it", kind, word, inst.Offset, flag))
		}
	}
	return diags
}

func isPushOrPop(inst scripting.Instruction) bool {
	base := inst.Word() & scripting.OpcodeDispatchMask
	return base == scripting.OP_PUSH_CONSTANT&scripting.OpcodeDispatchMask ||
		base == scripting.OP_POP_STATIC&scripting.OpcodeDispatchMask
}

// ── StackLimitRule ─────────────────────────────────────────────────────────

// StackLimitRule reports scripts that use more than the game's 32 stack
// slots: more than 32 STACK_ALLOC instructions, or local slots plus pending
// values beyond 32 at some point. The game does not check and overwrites
// the memory after the script context.
type StackLimitRule struct{}

func (r *StackLimitRule) Name() string { return "ta-stack-limit" }

func (r *StackLimitRule) Check(info *FileInfo) []Diagnostic {
	if !checksTA(info) {
		return nil
	}
	var diags []Diagnostic
	for _, si := range info.Scripts {
		if si.Stack.Allocs > scripting.StackSlots {
			diags = append(diags, taDiagnostic(r.Name(), info, si,
				"%d STACK_ALLOC instructions exceed the game's %d stack slots", si.Stack.Allocs, scripting.StackSlots))
			continue
		}
		for _, issue := range si.Stack.Issues {
			if issue.Kind == scripting.StackOverflow {
				diags = append(diags, taDiagnostic(r.Name(), info, si,
					"%d stack slots in use at 0x%04X exceed the game's %d", issue.Need, issue.Offset, scripting.StackSlots))
				break
			}
		}
	}
	return diags
}

// ── GetArgumentsRule ───────────────────────────────────────────────────────

// GetArgumentsRule reports GET instructions reached with fewer than five
// pending values. GET always pops a port and four arguments, so a
// one-argument get takes the other four from the function's locals and
// overwrites them with its result.
type GetArgumentsRule struct{}

func (r *GetArgumentsRule) Name() string { return "ta-get-arguments" }

func (r *GetArgumentsRule) Check(info *FileInfo) []Diagnostic {
	if !checksTA(info) {
		return nil
	}
	var diags []Diagnostic
	for _, si := range info.Scripts {
		for _, issue := range si.Stack.Issues {
			if issue.Kind == scripting.StackUnderflow && issue.Opcode == scripting.OP_GET {
				diags = append(diags, taDiagnostic(r.Name(), info, si,
					"GET at 0x%04X pops a port and 4 arguments but only %d values are pending; "+
						"the rest come from the function's local variables", issue.Offset, issue.Have))
			}
		}
	}
	return diags
}

// ── StackUnderflowRule ─────────────────────────────────────────────────────

// StackUnderflowRule reports other instructions that pop more values than
// the preceding code pushed, and control-flow joins reached with different
// stack depths.
type StackUnderflowRule struct{}

func (r *StackUnderflowRule) Name() string { return "ta-stack-underflow" }

func (r *StackUnderflowRule) Check(info *FileInfo) []Diagnostic {
	if !checksTA(info) {
		return nil
	}
	var diags []Diagnostic
	for _, si := range info.Scripts {
		for _, issue := range si.Stack.Issues {
			switch {
			case issue.Kind == scripting.StackUnderflow && issue.Opcode != scripting.OP_GET:
				diags = append(diags, taDiagnostic(r.Name(), info, si,
					"%s at 0x%04X pops %d values but only %d are pending",
					scripting.OpcodeName(issue.Opcode), issue.Offset, issue.Need, issue.Have))
			case issue.Kind == scripting.StackMismatch:
				diags = append(diags, taDiagnostic(r.Name(), info, si,
					"paths reach 0x%04X with %d and %d pending values", issue.Offset, issue.Have, issue.Need))
			}
		}
	}
	return diags
}

// ── DiscardCallRule ────────────────────────────────────────────────────────

// DiscardCallRule reports DISCARD_CALL (0x10063000) instructions with more
// than four arguments; the game copies them into a four-word buffer.
type DiscardCallRule struct{}

func (r *DiscardCallRule) Name() string { return "ta-discard-call" }

func (r *DiscardCallRule) Check(info *FileInfo) []Diagnostic {
	if !checksTA(info) {
		return nil
	}
	var diags []Diagnostic
	for _, si := range info.Scripts {
		for _, inst := range si.Instructions {
			if inst.Opcode == scripting.OP_DISCARD_CALL && (inst.Operand2 < 0 || inst.Operand2 > 4) {
				diags = append(diags, taDiagnostic(r.Name(), info, si,
					"DISCARD_CALL at 0x%04X has %d arguments; the game's buffer holds 4", inst.Offset, inst.Operand2))
			}
		}
	}
	return diags
}

// ── DuplicateFunctionRule ──────────────────────────────────────────────────

// DuplicateFunctionRule reports script names that appear more than once.
// The game looks functions up by name and always finds the first.
type DuplicateFunctionRule struct{}

func (r *DuplicateFunctionRule) Name() string { return "duplicate-function" }

func (r *DuplicateFunctionRule) Check(info *FileInfo) []Diagnostic {
	first := make(map[string]int)
	var diags []Diagnostic
	for i, name := range info.COB.ScriptNames {
		if name == "" {
			continue
		}
		if j, dup := first[name]; dup {
			diags = append(diags, Diagnostic{
				Rule:     r.Name(),
				Severity: Warning,
				Script:   name,
				Line:     info.ScriptStartLines[name],
				Message:  fmt.Sprintf("script %d has the same name as script %d; the game only calls the first", i, j),
			})
			continue
		}
		first[name] = i
	}
	return diags
}

// ── MalformedCOBRule ───────────────────────────────────────────────────────

// MalformedCOBRule reports the damage LoadFromReader tolerated (see
// scripting.COB.Warnings) and scripts whose code is truncated.
type MalformedCOBRule struct{}

func (r *MalformedCOBRule) Name() string { return "malformed-cob" }

func (r *MalformedCOBRule) Check(info *FileInfo) []Diagnostic {
	var diags []Diagnostic
	for _, w := range info.COB.Warnings {
		diags = append(diags, Diagnostic{
			Rule:     r.Name(),
			Severity: Warning,
			Message:  w.String(),
		})
	}
	for _, si := range info.Scripts {
		if si.DisassembleErr != nil {
			diags = append(diags, Diagnostic{
				Rule:     r.Name(),
				Severity: Error,
				Script:   si.Name,
				Line:     info.ScriptStartLines[si.Name],
				Message:  si.DisassembleErr.Error(),
			})
		}
	}
	return diags
}
