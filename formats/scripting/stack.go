package scripting

// StackSlots is the number of 32-bit stack slots a script context has in
// the game. Local variables (one slot per STACK_ALLOC, including the slots
// for parameters) and pending expression values share them; the game does
// not check the limit and overwrites the memory after the context when a
// script goes past it.
const StackSlots = 32

// StackIssueKind classifies a problem found by AnalyzeStack.
type StackIssueKind int

const (
	// StackUnderflow: the instruction pops more values than the preceding
	// expressions left on the stack. The game then takes local-variable
	// slots as operands (or, with an empty stack, reads below it).
	StackUnderflow StackIssueKind = iota
	// StackOverflow: local slots plus pending values exceed StackSlots.
	// Only the first instruction that goes over is reported.
	StackOverflow
	// StackMismatch: two control-flow paths reach the instruction with
	// different stack depths.
	StackMismatch
)

func (k StackIssueKind) String() string {
	switch k {
	case StackUnderflow:
		return "underflow"
	case StackOverflow:
		return "overflow"
	case StackMismatch:
		return "mismatch"
	default:
		return "unknown"
	}
}

// StackIssue is one problem found by AnalyzeStack.
type StackIssue struct {
	Kind   StackIssueKind
	Index  int    // index of the instruction in the analysed slice
	Offset uint32 // byte offset of the instruction in the code section
	Opcode uint32 // canonical opcode of the instruction
	// Need and Have are the values popped and the expression values
	// available (underflow), or the slots in use and StackSlots (overflow).
	Need, Have int
}

// StackReport summarises the stack use of one script.
type StackReport struct {
	Allocs int // STACK_ALLOC instructions in the script
	Locals int // slots reserved by STACK_ALLOC while no expression value was pending
	Peak   int // most slots in use at once: reserved slots plus pending values
	Issues []StackIssue
}

// AnalyzeStack follows every control-flow path through a script's
// instructions (as returned by COB.Disassemble) and tracks how many stack
// slots are in use. STACK_ALLOC executed while no expression value is
// pending reserves a local slot; every other instruction pops and pushes
// according to its OpcodeInfo. A path ends at RETURN, at a jump that leaves
// the script and at any instruction the game faults on: words no game
// executes, a PUSH or POP with an invalid flag, and, unless kingdoms is
// set, the TA: Kingdoms extensions.
func AnalyzeStack(insts []Instruction, kingdoms bool) StackReport {
	var report StackReport
	for _, inst := range insts {
		if inst.Opcode == OP_STACK_ALLOC {
			report.Allocs++
		}
	}

	byOffset := make(map[uint32]int, len(insts))
	for i, inst := range insts {
		byOffset[inst.Offset] = i
	}

	type state struct{ locals, depth int }
	type path struct {
		index int
		state state
	}
	seen := make([]*state, len(insts))
	reported := make(map[[2]int]bool)
	overflowReported := false
	addIssue := func(issue StackIssue) {
		key := [2]int{issue.Index, int(issue.Kind)}
		if reported[key] {
			return
		}
		reported[key] = true
		report.Issues = append(report.Issues, issue)
	}

	work := []path{{0, state{}}}
	for len(work) > 0 {
		p := work[len(work)-1]
		work = work[:len(work)-1]
		for i, st := p.index, p.state; i >= 0 && i < len(insts); {
			inst := insts[i]
			if prev := seen[i]; prev != nil {
				if prev.depth != st.depth {
					addIssue(StackIssue{Kind: StackMismatch, Index: i, Offset: inst.Offset, Opcode: inst.Opcode,
						Need: st.depth, Have: prev.depth})
				}
				break
			}
			entry := st
			seen[i] = &entry

			info, ok := LookupOpcode(inst.Word())
			if !ok || info.Faults || (info.Kingdoms && !kingdoms) {
				break
			}
			if inst.Opcode == OP_STACK_ALLOC && st.depth == 0 {
				st.locals++
				if st.locals > report.Locals {
					report.Locals = st.locals
				}
			} else {
				pops, _, _ := inst.StackEffect()
				if pops < 0 || pops > st.depth {
					addIssue(StackIssue{Kind: StackUnderflow, Index: i, Offset: inst.Offset, Opcode: inst.Opcode,
						Need: pops, Have: st.depth})
					st.depth = 0
				} else {
					st.depth -= pops
				}
				st.depth += info.Pushes
			}
			if slots := st.locals + st.depth; slots > report.Peak {
				report.Peak = slots
			}
			if slots := st.locals + st.depth; slots > StackSlots && !overflowReported {
				overflowReported = true
				addIssue(StackIssue{Kind: StackOverflow, Index: i, Offset: inst.Offset, Opcode: inst.Opcode,
					Need: slots, Have: StackSlots})
			}

			switch inst.Opcode {
			case OP_RETURN:
				i = -1
				continue
			case OP_JUMP:
				target, found := byOffset[uint32(inst.Operand)*4]
				if !found {
					i = -1
					continue
				}
				i = target
				continue
			case OP_JUMP_IF_FALSE:
				if target, found := byOffset[uint32(inst.Operand)*4]; found {
					work = append(work, path{target, st})
				}
			}
			i++
		}
	}
	return report
}
