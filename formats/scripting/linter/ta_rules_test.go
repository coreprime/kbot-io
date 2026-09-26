package linter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/scripting"
	"github.com/coreprime/kbot-io/formats/scripting/assembly"
	"github.com/coreprime/kbot-io/testutil"
)

func assembleCOB(t *testing.T, version int, body string) *scripting.COB {
	t.Helper()
	cob, err := assembly.NewAssembler().Assemble(fmt.Sprintf(".version %d\n.piece base\n.script Create\n%s", version, body))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return cob
}

func rulesFired(diags []Diagnostic) map[string]int {
	fired := make(map[string]int)
	for _, d := range diags {
		fired[d.Rule]++
	}
	return fired
}

func TestTACompatRules(t *testing.T) {
	tests := []struct {
		rule string
		body string
	}{
		{"ta-kingdoms-opcode", "0000 PUSH_CONST 3\n0008 PLAY_SOUND 100\n0010 POP_STACK\n0014 PUSH_CONST 0\n001C RETURN\n"},
		{"ta-kingdoms-opcode", "0000 PUSH_CONST 3\n0008 PUSH_CONST 2\n0010 TAK_MATH_0A\n0014 RETURN\n"},
		{"ta-kingdoms-opcode", "0000 PUSH_CONST 3\n0008 MISSION_COMMAND 0, 1\n0014 RETURN\n"},
		{"ta-unknown-opcode", "0000 UNKNOWN_0x10090000\n0004 RETURN\n"},
		{"ta-push-flags", "0000 PUSH_IMM 5\n0008 POP_STACK\n000C RETURN\n"},
		{"ta-push-flags", "0000 PUSH_IMM@0x10021008 5\n0008 RETURN\n"},
		{"ta-push-flags", "0000 PUSH_CONST 5\n0008 UNKNOWN_0x10023001 0\n0010 RETURN\n"},
		{"ta-get-arguments", "0000 STACK_ALLOC\n0004 PUSH_CONST 4\n000C GET\n0010 POP_LOCAL 0\n0018 RETURN\n"},
		{"ta-stack-underflow", "0000 PUSH_CONST 4\n0008 ADD\n000C RETURN\n"},
		{"ta-discard-call", "0000 DISCARD_CALL 0, 5\n000C RETURN\n"},
		{"ta-stack-limit", strings.Repeat("0000 STACK_ALLOC\n", 33) + "0000 RETURN\n"},
	}
	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			cob := assembleCOB(t, 4, tt.body)
			diags := New().Lint(cob)
			fired := rulesFired(diags)
			if fired[tt.rule] == 0 {
				t.Errorf("%s did not fire; got %v", tt.rule, fired)
			}
			for _, d := range diags {
				if d.Rule == tt.rule && d.Severity != Error {
					t.Errorf("%s severity %v, want error", tt.rule, d.Severity)
				}
			}

			// A TA: Kingdoms file is not checked against TA.
			kingdoms := assembleCOB(t, 6, tt.body)
			for rule := range rulesFired(New().Lint(kingdoms)) {
				if strings.HasPrefix(rule, "ta-") {
					t.Errorf("version 6: %s fired", rule)
				}
			}
		})
	}
}

func TestTACompatRulesAcceptCleanCode(t *testing.T) {
	cob := assembleCOB(t, 4, `0000 STACK_ALLOC
0004 PUSH_CONST 7
000C PUSH_CONST 0
0014 PUSH_CONST 0
001C PUSH_CONST 0
0024 PUSH_CONST 0
002C GET
0030 PUSH_CONST 3
0038 XOR
003C NOT
0040 POP_LOCAL 0
0048 PUSH_CONST 0
0050 RETURN
`)
	for _, d := range NewWithRules(TACompatRules()...).Lint(cob) {
		t.Errorf("unexpected %s", d)
	}
}

func TestDuplicateFunctionRule(t *testing.T) {
	cob, err := assembly.NewAssembler().Assemble(".version 4\n.script Walk\n0000 PUSH_CONST 0\n0008 RETURN\n.script Walk\n0000 PUSH_CONST 1\n0008 RETURN\n")
	if err != nil {
		t.Fatal(err)
	}
	if rulesFired(NewWithRules(&DuplicateFunctionRule{}).Lint(cob))["duplicate-function"] != 1 {
		t.Error("duplicate-function did not fire once")
	}
}

func TestMalformedCOBRule(t *testing.T) {
	cob := assembleCOB(t, 4, "0000 PUSH_CONST 0\n0008 RETURN\n")
	var buf bytes.Buffer
	if err := cob.WriteToWriter(&buf); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// Point the piece name at offset 0 and cut the final operand word.
	pieceTable := binary.LittleEndian.Uint32(data[32:])
	binary.LittleEndian.PutUint32(data[pieceTable:], 0)
	reread, err := scripting.LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	reread.Code = reread.Code[:4] // PUSH_CONST without its operand
	fired := rulesFired(NewWithRules(&MalformedCOBRule{}).Lint(reread))
	if fired["malformed-cob"] != 2 {
		t.Errorf("malformed-cob fired %d times, want 2 (name offset, truncated code)", fired["malformed-cob"])
	}
}

// TestRetailScriptsPassTACompat checks the TA rules against every retail
// TA script: none of them may fire.
func TestRetailScriptsPassTACompat(t *testing.T) {
	dir := testutil.UnpackedDir(t, "scripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	l := NewWithRules(append(TACompatRules(), &MalformedCOBRule{})...)
	files := 0
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".cob") {
			continue
		}
		cob, err := scripting.LoadFromFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		files++
		for _, d := range l.Lint(cob) {
			t.Errorf("%s: %s", e.Name(), d)
		}
	}
	if files == 0 {
		t.Fatal("no retail scripts found")
	}
}
