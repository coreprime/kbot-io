package scripting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

// program decodes a word list into instructions the way Disassemble does.
func program(t *testing.T, ws ...uint32) []Instruction {
	t.Helper()
	cob := &COB{NumScripts: 1, ScriptCodeIndices: []uint32{0}, Code: words(ws...)}
	insts, err := cob.Disassemble(0)
	if err != nil {
		t.Fatal(err)
	}
	return insts
}

func TestAnalyzeStackBalancedScript(t *testing.T) {
	insts := program(t,
		OP_STACK_ALLOC, OP_STACK_ALLOC, // two locals
		OP_PUSH_LOCAL_VAR, 0, OP_PUSH_CONSTANT, 3, OP_LESS_THAN, // words 2-6
		OP_JUMP_IF_FALSE, 13, // words 7-8: skip to word 13 when false
		OP_PUSH_CONSTANT, 1, OP_POP_LOCAL_VAR, 1, // words 9-12
		OP_PUSH_CONSTANT, 0, OP_RETURN, // words 13-15
	)
	report := AnalyzeStack(insts, false)
	if len(report.Issues) != 0 {
		t.Errorf("issues %+v", report.Issues)
	}
	if report.Allocs != 2 || report.Locals != 2 || report.Peak != 4 {
		t.Errorf("allocs %d locals %d peak %d", report.Allocs, report.Locals, report.Peak)
	}
}

func TestAnalyzeStackOneArgumentGet(t *testing.T) {
	// `x = get(4);` as earlier compilers wrote it: one value, then GET.
	insts := program(t,
		OP_STACK_ALLOC, OP_STACK_ALLOC, OP_STACK_ALLOC, OP_STACK_ALLOC,
		OP_PUSH_CONSTANT, 4, OP_GET, OP_POP_LOCAL_VAR, 0,
		OP_PUSH_CONSTANT, 0, OP_RETURN,
	)
	report := AnalyzeStack(insts, false)
	if len(report.Issues) != 1 {
		t.Fatalf("issues %+v", report.Issues)
	}
	issue := report.Issues[0]
	if issue.Kind != StackUnderflow || issue.Opcode != OP_GET || issue.Need != 5 || issue.Have != 1 {
		t.Errorf("issue %+v", issue)
	}
}

func TestAnalyzeStackOverflow(t *testing.T) {
	ws := make([]uint32, 0, 40)
	for i := 0; i < 33; i++ {
		ws = append(ws, OP_STACK_ALLOC)
	}
	ws = append(ws, OP_PUSH_CONSTANT, 0, OP_RETURN)
	report := AnalyzeStack(program(t, ws...), false)
	if report.Allocs != 33 || report.Peak != 34 {
		t.Errorf("allocs %d peak %d", report.Allocs, report.Peak)
	}
	if len(report.Issues) != 1 || report.Issues[0].Kind != StackOverflow {
		t.Errorf("issues %+v", report.Issues)
	}
}

func TestAnalyzeStackMismatchedJoin(t *testing.T) {
	// One path pushes an extra value before joining the other.
	insts := program(t,
		OP_PUSH_CONSTANT, 1, OP_JUMP_IF_FALSE, 6,
		OP_PUSH_CONSTANT, 7, // words 4-5
		OP_PUSH_CONSTANT, 0, OP_RETURN, // word 6: join
	)
	report := AnalyzeStack(insts, false)
	if len(report.Issues) != 1 || report.Issues[0].Kind != StackMismatch {
		t.Errorf("issues %+v", report.Issues)
	}
}

func TestAnalyzeStackStopsAtKingdomsInstructionsForTA(t *testing.T) {
	// PLAY_SOUND pops one value and pushes its result in TA: Kingdoms; TA
	// faults on it, so nothing after it is analysed for a TA script.
	insts := program(t, OP_PUSH_CONSTANT, 3, OP_PLAY_SOUND, 100, OP_POP_STACK, OP_POP_STACK, OP_RETURN)
	if r := AnalyzeStack(insts, false); len(r.Issues) != 0 {
		t.Errorf("TA: issues %+v", r.Issues)
	}
	if r := AnalyzeStack(insts, true); len(r.Issues) != 1 || r.Issues[0].Kind != StackUnderflow {
		t.Errorf("TA: Kingdoms: issues %+v", r.Issues)
	}
}

// TestRetailScriptsHaveBalancedStacks checks the stack model against the
// retail scripts: every TA script, and every TA: Kingdoms script with the
// TA: Kingdoms math operators taken as binary operators, balances.
func TestRetailScriptsHaveBalancedStacks(t *testing.T) {
	check := func(t *testing.T, dir string, kingdoms bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		files := 0
		for _, e := range entries {
			if !strings.EqualFold(filepath.Ext(e.Name()), ".cob") {
				continue
			}
			cob, err := LoadFromFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("%s: %v", e.Name(), err)
			}
			files++
			for i := 0; i < int(cob.NumScripts); i++ {
				insts, err := cob.Disassemble(i)
				if err != nil {
					t.Fatalf("%s script %d: %v", e.Name(), i, err)
				}
				report := AnalyzeStack(insts, kingdoms)
				if len(report.Issues) != 0 || report.Peak > StackSlots {
					t.Errorf("%s %s: peak %d issues %+v", e.Name(), cob.ScriptNames[i], report.Peak, report.Issues)
				}
			}
		}
		if files == 0 {
			t.Fatalf("no .cob files in %s", dir)
		}
	}
	t.Run("TA", func(t *testing.T) { check(t, testutil.UnpackedDir(t, "scripts"), false) })
	t.Run("Kingdoms units", func(t *testing.T) { check(t, testutil.TAKUnpackedDir(t, "scripts"), true) })
	t.Run("Kingdoms missions", func(t *testing.T) { check(t, testutil.TAKUnpackedDir(t, "missions"), true) })
}
