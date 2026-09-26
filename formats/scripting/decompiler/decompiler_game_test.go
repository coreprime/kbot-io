package decompiler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/scripting"
	"github.com/coreprime/kbot-io/formats/scripting/assembly"
	"github.com/coreprime/kbot-io/formats/scripting/compiler"
)

func assemble(t *testing.T, listing string) *scripting.COB {
	t.Helper()
	cob, err := assembly.NewAssembler().Assemble(listing)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return cob
}

func writeCOB(t *testing.T, cob *scripting.COB) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := cob.WriteToWriter(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	return buf.Bytes()
}

// roundTrip decompiles a COB, checks the BOS contains each fragment, then
// compiles it back and requires identical bytes.
func roundTrip(t *testing.T, cob *scripting.COB, fragments ...string) string {
	t.Helper()
	bos, err := NewDecompiler(cob).Decompile()
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	for _, f := range fragments {
		if !strings.Contains(bos, f) {
			t.Errorf("BOS lacks %q:\n%s", f, bos)
		}
	}
	recompiled, err := compiler.NewCompiler(bos).Compile()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, bos)
	}
	if !bytes.Equal(writeCOB(t, cob), writeCOB(t, recompiled)) {
		t.Errorf("recompiled bytes differ\n%s", bos)
	}
	return bos
}

func TestDecompileBitwiseAndNewGameInstructions(t *testing.T) {
	cob := assemble(t, `.version 4
.piece base
.script Create
0000  STACK_ALLOC
0004  PUSH_LOCAL           0
000C  PUSH_CONST           3
0014  XOR
0018  NOT
001C  PUSH_CONST           6
0024  XOR_ALT
0028  POP_LOCAL            0
0030  PUSH_LOCAL           0
0038  IS_CARRYING_UNIT
003C  CARRIER_UNIT_ID
0040  DISCARD_CALL         9, 2
004C  PUSH_CONST           1
0054  PUSH_CONST           2
005C  PIECE_OP_09          0
0064  SHADE                0
006C  PUSH_CONST           0
0074  RETURN
`)
	roundTrip(t, cob,
		"local_0 = (~(local_0 ^ 3) XOR 6);",
		"__discard_call(9, __is_carrying_unit(local_0), __carrier_unit_id());",
		"__piece_op_09(base, 1, 2);",
		"shade base;",
	)
}

func TestDecompileWritesDegreesAndDistances(t *testing.T) {
	cob := assemble(t, `.version 4
.piece base
.script Create
0000  PUSH_CONST           9102
0008  PUSH_CONST           6371
0010  TURN                 0, 1
001C  PUSH_CONST           81920000
0024  PUSH_CONST           -393216
002C  MOVE                 0, 2
0038  PUSH_CONST           0
0040  RETURN
`)
	roundTrip(t, cob,
		".angle_units degrees",
		"turn base to y-axis <35> speed <50>;",
		"move base to z-axis [-2.4] speed [500];",
	)
}

func TestDecompiledAnglesSurviveWithoutTheDirective(t *testing.T) {
	// Removing `.angle_units degrees` must not turn <35> into a raw 35.
	cob := assemble(t, `.version 4
.piece base
.script Create
0000  PUSH_CONST           6371
0008  TURN_NOW             0, 1
0014  PUSH_CONST           0
001C  RETURN
`)
	bos, err := NewDecompiler(cob).Decompile()
	if err != nil {
		t.Fatal(err)
	}
	bos = strings.Replace(bos, ".angle_units degrees\n", "", 1)
	recompiled, err := compiler.NewCompiler(bos).Compile()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(writeCOB(t, cob), writeCOB(t, recompiled)) {
		t.Errorf("recompiled bytes differ\n%s", bos)
	}
}

func TestDecompileKingdomsMathIsBinary(t *testing.T) {
	cob := assemble(t, `.version 6
.script Create
0000  STACK_ALLOC
0004  PUSH_CONST           3
000C  PUSH_LOCAL           0
0014  PUSH_CONST           2
001C  MUL
0020  TAK_MATH_09
0024  PUSH_CONST           3
002C  TAK_MATH_0A
0030  PUSH_CONST           9
0038  TAK_MATH_0B
003C  POP_LOCAL            0
0044  STACK_ALLOC
0048  RETURN
`)
	roundTrip(t, cob, "local_0 = __tak_math_0b(__tak_math_0a(__tak_math_09(3, (local_0 * 2)), 3), 9);")
}

func TestDecompileCarriesHeaderField5(t *testing.T) {
	cob := assemble(t, ".version 4\n.field5 42\n.script Create\n0000  PUSH_CONST 0\n0008  RETURN\n")
	if cob.UKZero != 42 {
		t.Fatalf("UKZero = %d", cob.UKZero)
	}
	roundTrip(t, cob, ".field5 42")
	listing, err := NewDecompiler(cob).Disassemble(assembly.Plain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, ".field5 42") {
		t.Errorf("listing lacks .field5:\n%s", listing)
	}
}

func TestDecompileRefusesInstructionsWithoutBOSForm(t *testing.T) {
	for name, word := range map[string]string{
		"unknown word":      "UNKNOWN_0x10090000",
		"flagless PUSH":     "PUSH_IMM@0x10021008",
		"POP with flag 1":   "UNKNOWN_0x10023001",
		"stack underflow":   "ADD",
		"one-argument GET*": "GET",
	} {
		t.Run(name, func(t *testing.T) {
			listing := ".version 4\n.script Create\n0000  PUSH_CONST 1\n0008  " + word + "\n000C  RETURN\n"
			if strings.HasPrefix(word, "PUSH_IMM") || strings.HasPrefix(word, "UNKNOWN_0x10023001") {
				listing = ".version 4\n.script Create\n0000  " + word + " 5\n0008  RETURN\n"
			}
			cob := assemble(t, listing)
			if _, err := NewDecompiler(cob).Decompile(); err == nil {
				t.Fatal("expected an error instead of silently wrong BOS")
			}
		})
	}
}

func TestDecompileUsesPlaceholderForBadScriptNames(t *testing.T) {
	cob := assemble(t, ".version 4\n.script Create\n0000  PUSH_CONST 0\n0008  RETURN\n")
	cob.ScriptNames[0] = "\x04"
	bos, err := NewDecompiler(cob).Decompile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bos, "script_0()") {
		t.Errorf("BOS:\n%s", bos)
	}
}

func TestDecompileRebuildsElseBlocks(t *testing.T) {
	src := `piece base;
static-var s;
Create()
{
	var x;
	if (s == 1)
	{
		x = 2;
	}
	else
	{
		x = 3;
	}
	while (x < 9)
	{
		if (s == 2)
		{
			x = x + 1;
		}
		else
		{
			if (s == 3)
			{
				x = x + 2;
			}
			else
			{
			}
		}
	}
	if (s == 4)
	{
		if (s == 5)
		{
			x = 5;
		}
		else
		{
		}
	}
	else
	{
		x = 6;
	}
	if (s == 6)
	{
		if (s == 7)
		{
			x = 7;
		}
		else
		{
		}
	}
	return x;
}
`
	cob, err := compiler.NewCompiler(src).Compile()
	if err != nil {
		t.Fatal(err)
	}
	bos := roundTrip(t, cob, "else")
	if n := strings.Count(bos, "else"); n != 6 {
		t.Errorf("BOS has %d else blocks, want 6:\n%s", n, bos)
	}
}

func TestDecompileRefusesJumpsOutsideBlocks(t *testing.T) {
	for name, body := range map[string]string{
		// A forward goto: dropping it would run the skipped statements.
		"forward JUMP": `0000  STACK_ALLOC
0004  PUSH_CONST           1
000C  PUSH_CONST           2
0014  ADD
0018  POP_LOCAL            0
0020  JUMP                 14
0028  PUSH_CONST           5
0030  POP_LOCAL            0
0038  PUSH_CONST           3
0040  POP_LOCAL            0
0048  PUSH_LOCAL           0
0050  RETURN
`,
		"forward JUMP, low-bit variant": `0000  STACK_ALLOC
0004  PUSH_CONST           1
000C  POP_LOCAL            0
0014  JUMP@0x10064001      10
001C  PUSH_CONST           5
0024  POP_LOCAL            0
002C  PUSH_LOCAL           0
0034  RETURN
`,
		"backward JUMP without a condition": `0000  STACK_ALLOC
0004  PUSH_CONST           1
000C  POP_LOCAL            0
0014  JUMP                 1
001C  PUSH_CONST           0
0024  RETURN
`,
		"loop JUMP before its condition": `0000  STACK_ALLOC
0004  PUSH_LOCAL           0
000C  JUMP_IF_FALSE        7
0014  JUMP                 0
001C  PUSH_CONST           0
0024  RETURN
`,
		"JUMP_IF_FALSE out of its loop": `0000  STACK_ALLOC
0004  PUSH_LOCAL           0
000C  JUMP_IF_FALSE        15
0014  PUSH_LOCAL           0
001C  JUMP_IF_FALSE        17
0024  PUSH_CONST           1
002C  POP_LOCAL            0
0034  JUMP                 1
003C  PUSH_CONST           0
0044  RETURN
`,
		"JUMP_IF_FALSE without a condition": `0000  STACK_ALLOC
0004  PUSH_CONST           1
000C  PUSH_CONST           2
0014  POP_LOCAL            0
001C  JUMP_IF_FALSE        9
0024  PUSH_CONST           0
002C  RETURN
`,
	} {
		t.Run(name, func(t *testing.T) {
			cob := assemble(t, ".version 4\n.script Create\n"+body)
			bos, err := NewDecompiler(cob).Decompile()
			if err == nil || !strings.Contains(err.Error(), "JUMP") {
				t.Fatalf("err = %v, want a jump error instead of:\n%s", err, bos)
			}
		})
	}
}
