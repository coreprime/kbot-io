package compiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/scripting"
)

// compileBody compiles a single Create() function with the given header
// lines (directives and declarations) and body, and returns the words of
// its code without the implicit `return 0` (PUSH_CONSTANT 0, RETURN) the
// compiler appends to a function that does not end with a return.
func compileBody(t *testing.T, header, body string) ([]uint32, *scripting.COB) {
	t.Helper()
	src := header + "\nCreate()\n{\n" + body + "\n}\n"
	cob, err := NewCompiler(src).Compile()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, src)
	}
	code := codeWords(cob)
	if n := len(code); n >= 3 && code[n-3] == push && code[n-2] == 0 && code[n-1] == scripting.OP_RETURN {
		code = code[:n-3]
	}
	return code, cob
}

func codeWords(cob *scripting.COB) []uint32 {
	out := make([]uint32, len(cob.Code)/4)
	for i := range out {
		c := cob.Code[i*4 : i*4+4]
		out[i] = uint32(c[0]) | uint32(c[1])<<8 | uint32(c[2])<<16 | uint32(c[3])<<24
	}
	return out
}

func compileError(t *testing.T, header, body string) error {
	t.Helper()
	_, err := NewCompiler(header + "\nCreate()\n{\n" + body + "\n}\n").Compile()
	return err
}

func equalWords(t *testing.T, got []uint32, want ...uint32) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("code\n got %s\nwant %s", hexWords(got), hexWords(want))
	}
}

func hexWords(ws []uint32) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = fmt.Sprintf("%X", w)
	}
	return strings.Join(parts, " ")
}

const (
	push   = scripting.OP_PUSH_CONSTANT
	local  = scripting.OP_PUSH_LOCAL_VAR
	popLoc = scripting.OP_POP_LOCAL_VAR
	alloc  = scripting.OP_STACK_ALLOC
)

func TestCompileBitwiseOperatorsUseTheGameOpcodes(t *testing.T) {
	code, _ := compileBody(t, "", "\tvar x, a, b;\n\tx = a ^ b;\n\tx = ~a;\n\tx = a & b;\n\tx = a XOR b;")
	equalWords(t, code,
		alloc, alloc, alloc,
		local, 1, local, 2, scripting.OP_XOR, popLoc, 0,
		local, 1, scripting.OP_NOT, popLoc, 0,
		local, 1, local, 2, scripting.OP_BITWISE_AND, popLoc, 0,
		local, 1, local, 2, scripting.OP_XOR_ALT, popLoc, 0,
	)
}

func TestCompileRejectsUnknownIdentifiers(t *testing.T) {
	for _, body := range []string{
		"\tvar x;\n\tx = nosuchthing;",
		"\tvar x;\n\tx = 1 + nosuchthing;",
		"\tsleep SIG_UNDEFINED;",
		"\tvar x;\n\tx = Helper(1);",
	} {
		if err := compileError(t, "", body); err == nil {
			t.Errorf("%q compiled; want an error", body)
		}
	}
}

func TestCompilePrecedenceIsLeftAssociative(t *testing.T) {
	code, _ := compileBody(t, "", "\tvar x;\n\tx = 3 * 5 / 2;\n\tx = 1 + 2 - 3;\n\tx = 1 + 2 * 3 == 7 && 1 < 2;")
	equalWords(t, code,
		alloc,
		push, 3, push, 5, scripting.OP_MUL, push, 2, scripting.OP_DIV, popLoc, 0,
		push, 1, push, 2, scripting.OP_ADD, push, 3, scripting.OP_SUB, popLoc, 0,
		push, 1, push, 2, push, 3, scripting.OP_MUL, scripting.OP_ADD, push, 7, scripting.OP_EQUAL,
		push, 1, push, 2, scripting.OP_LESS_THAN, scripting.OP_LOGICAL_AND, popLoc, 0,
	)
}

func TestCompileKeywordOperators(t *testing.T) {
	code, _ := compileBody(t, "", "\tvar a, b, x;\n\tx = NOT a OR a AND b;\n\tx = -a;\n\tx = -5;")
	equalWords(t, code,
		alloc, alloc, alloc,
		local, 0, scripting.OP_LOGICAL_NOT, local, 0, local, 1, scripting.OP_LOGICAL_AND, scripting.OP_LOGICAL_OR, popLoc, 2,
		push, 0, local, 0, scripting.OP_SUB, popLoc, 2,
		push, uint32(0xFFFFFFFB), popLoc, 2,
	)
}

func TestCompileIntegerRange(t *testing.T) {
	code, _ := compileBody(t, "", "\tvar x;\n\tx = -2147483648;\n\tx = 2147483647;\n\tx = 0xFFFFFFFF;")
	equalWords(t, code, alloc, push, 0x80000000, popLoc, 0, push, 0x7FFFFFFF, popLoc, 0, push, 0xFFFFFFFF, popLoc, 0)
	if err := compileError(t, "", "\tvar x;\n\tx = 4294967296;"); err == nil {
		t.Error("a 33-bit literal compiled")
	}
}

func TestCompileAngleAndDistanceLiterals(t *testing.T) {
	code, _ := compileBody(t, "piece base;",
		"\tturn base to y-axis <35> speed <50>;\n\tmove base to z-axis [-2.4] speed [500];")
	equalWords(t, code,
		push, 9102, push, 6371, scripting.OP_TURN, 0, 1,
		push, 81920000, push, uint32(0xFFFA0000), scripting.OP_MOVE, 0, 2,
	)
}

func TestCompileAngleLiteralExpressions(t *testing.T) {
	// Each <n> is its own literal; the brackets are not stripped as a
	// wrapper around the whole operand.
	code, _ := compileBody(t, "piece base;", "\tturn base to x-axis <5> + <3> speed <90>;")
	equalWords(t, code, push, 16384, push, 910, push, 546, scripting.OP_ADD, scripting.OP_TURN, 0, 0)
	// Brackets around a whole expression, as earlier decompilers wrote,
	// are still dropped.
	code, _ = compileBody(t, "piece base;", "\tvar x;\n\tturn base to x-axis <x> now;")
	equalWords(t, code, alloc, local, 0, scripting.OP_TURN_NOW, 0, 0)
}

func TestCompileRawAngleUnits(t *testing.T) {
	// `.angle_units raw`, and sources written by earlier decompilers, keep
	// <n> as the value itself.
	code, _ := compileBody(t, ".angle_units raw\npiece base;", "\tturn base to y-axis <16384> speed <(1 + 2)>;")
	equalWords(t, code, push, 1, push, 2, scripting.OP_ADD, push, 16384, scripting.OP_TURN, 0, 1)
	code, _ = compileBody(t, "// Decompiled from COB bytecode\npiece base;", "\tturn base to y-axis <16384> now;")
	equalWords(t, code, push, 16384, scripting.OP_TURN_NOW, 0, 1)
}

func TestCompilePieceNamesAreTheirIndex(t *testing.T) {
	code, _ := compileBody(t, "piece base, turret, flare;", "\tvar piecenum;\n\tpiecenum = flare;")
	equalWords(t, code, alloc, push, 2, popLoc, 0)
}

func TestCompileGetAlwaysPushesFiveValues(t *testing.T) {
	code, _ := compileBody(t, "piece base, turret;", strings.Join([]string{
		"\tvar x, unitid;",
		"\tx = get(4);",               // earlier one-argument form: padded
		"\tx = get PIECE_XZ(turret);", // retail form
		"\tx = get(15, 1, 2, 0, 0);",  // full form
		"\tx = get HEALTH;",           // unit value
		"\tx = get (1 + 2);",          // `get (expr)` is a unit value of expr
	}, "\n"))
	equalWords(t, code,
		alloc, alloc,
		push, 4, push, 0, push, 0, push, 0, push, 0, scripting.OP_GET, popLoc, 0,
		push, 7, push, 1, push, 0, push, 0, push, 0, scripting.OP_GET, popLoc, 0,
		push, 15, push, 1, push, 2, push, 0, push, 0, scripting.OP_GET, popLoc, 0,
		push, 4, scripting.OP_GET_UNIT_VALUE, popLoc, 0,
		push, 1, push, 2, scripting.OP_ADD, scripting.OP_GET_UNIT_VALUE, popLoc, 0,
	)
	if err := compileError(t, "", "\tvar x;\n\tx = get(1, 2, 3, 4, 5, 6);"); err == nil {
		t.Error("get with six values compiled")
	}
	if err := compileError(t, "", "\tvar x;\n\tx = get();"); err == nil {
		t.Error("get() compiled")
	}
}

func TestCompileLimitsLocalsToTheGameStack(t *testing.T) {
	names := make([]string, 33)
	for i := range names {
		names[i] = fmt.Sprintf("v%d", i)
	}
	err := compileError(t, "", "\tvar "+strings.Join(names, ", ")+";")
	if err == nil || !strings.Contains(err.Error(), "32") {
		t.Fatalf("33 locals: err = %v", err)
	}
	// 32 locals fill the stack: `return;` (a bare RETURN) fits, the
	// implicit `return 0` needs a 33rd slot for its value.
	if _, err := NewCompiler("Create()\n{\n\tvar " + strings.Join(names[:32], ", ") + ";\n\treturn;\n}\n").Compile(); err != nil {
		t.Fatalf("32 locals: %v", err)
	}
	if _, err := NewCompiler("Create()\n{\n\tvar " + strings.Join(names[:32], ", ") + ";\n}\n").Compile(); err == nil {
		t.Fatal("32 locals plus the implicit return value compiled")
	}

	// 30 locals plus an expression four values deep needs 34 slots.
	body := "\tvar " + strings.Join(names[:30], ", ") + ";\n\tv0 = 1 + (2 + (3 + 4));"
	if err := compileError(t, "", body); err == nil || !strings.Contains(err.Error(), "stack slots") {
		t.Fatalf("deep expression: err = %v", err)
	}
}

func TestCompileRejectsDuplicateLocals(t *testing.T) {
	if _, err := NewCompiler("Create(a)\n{\n\tvar a;\n}\n").Compile(); err == nil {
		t.Fatal("a parameter redeclared as a local compiled")
	}
}

func TestCompileKingdomsConstructsNeedVersion6(t *testing.T) {
	for _, tc := range []struct{ header, body string }{
		{"", "\tplay-sound(3, 100);"},
		{"", "\tvar x;\n\tx = play-sound(3, 100);"},
		{`.sound_name "cmd"`, "\tMission-Command(\"cmd\", 1);"},
		{"", "\tvar x;\n\tx = __tak_math_09(3, 2);"},
		{"", "\tvar x;\n\tx = __tak_math_0a(3, 2);"},
		{"", "\tvar x;\n\tx = __tak_math_0b(3, 2);"},
	} {
		if err := compileError(t, tc.header, tc.body); err == nil || !strings.Contains(err.Error(), ".version 6") {
			t.Errorf("%q in a TA script: err = %v", tc.body, err)
		}
		if err := compileError(t, ".version 6\n"+tc.header, tc.body); err != nil {
			t.Errorf("%q with .version 6: %v", tc.body, err)
		}
	}
	// TA runs dont-shadow (as a no-op), so it is allowed without .version 6.
	code, _ := compileBody(t, "piece base;", "\tdont-shadow(base);")
	equalWords(t, code, scripting.OP_DONT_SHADOW, 0)
}

func TestCompileDuplicateFunctionsBindToTheFirst(t *testing.T) {
	src := "Helper()\n{\n\treturn 1;\n}\nHelper()\n{\n\treturn 2;\n}\nCreate()\n{\n\tcall-script Helper();\n}\n"
	c := NewCompiler(src)
	cob, err := c.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if cob.NumScripts != 3 {
		t.Fatalf("NumScripts = %d, want both definitions kept", cob.NumScripts)
	}
	if len(c.Warnings()) != 1 {
		t.Errorf("warnings = %v", c.Warnings())
	}
	insts, err := cob.Disassemble(2)
	if err != nil {
		t.Fatal(err)
	}
	if insts[0].Opcode != scripting.OP_CALL_SCRIPT || insts[0].Operand != 0 {
		t.Errorf("call-script Helper -> %v, want script 0", insts[0])
	}
}

func TestCompileCallsFunctionsDefinedLater(t *testing.T) {
	src := "Create()\n{\n\tstart-script Later(1, 2);\n}\nLater(a, b)\n{\n\treturn a;\n}\n"
	cob, err := NewCompiler(src).Compile()
	if err != nil {
		t.Fatal(err)
	}
	insts, _ := cob.Disassemble(0)
	if insts[2].Opcode != scripting.OP_START_SCRIPT || insts[2].Operand != 1 || insts[2].Operand2 != 2 {
		t.Errorf("start-script Later -> %v", insts[2])
	}
}

func TestCompileGameInstructionsWithoutKeywords(t *testing.T) {
	code, _ := compileBody(t, "piece base;", strings.Join([]string{
		"\tvar x;",
		"\tx = __is_carrying_unit(x);",
		"\tx = __carrier_unit_id();",
		"\t__discard_call(5, 1, 2);",
		"\t__piece_op_09(base, 1, 2);",
		"\tshade base;",
	}, "\n"))
	equalWords(t, code,
		alloc,
		local, 0, scripting.OP_IS_CARRYING_UNIT, popLoc, 0,
		scripting.OP_CARRIER_UNIT_ID, popLoc, 0,
		push, 1, push, 2, scripting.OP_DISCARD_CALL, 5, 2,
		push, 1, push, 2, scripting.OP_PIECE_OP_09, 0,
		scripting.OP_SHADE, 0,
	)
	if err := compileError(t, "", "\t__discard_call(0, 1, 2, 3, 4, 5);"); err == nil {
		t.Error("__discard_call with five arguments compiled")
	}
}

func TestCompileEndsFunctionsWithReturn(t *testing.T) {
	cob, err := NewCompiler("Create()\n{\n\tsleep 1;\n}\nWalk()\n{\n\treturn 5;\n}\nIdle()\n{\n}\n").Compile()
	if err != nil {
		t.Fatal(err)
	}
	// A function that would run on into the next one gets `return 0`; one
	// that already ends in RETURN is left alone.
	equalWords(t, codeWords(cob),
		push, 1, scripting.OP_SLEEP, push, 0, scripting.OP_RETURN,
		push, 5, scripting.OP_RETURN,
		push, 0, scripting.OP_RETURN,
	)

	kingdoms, err := NewCompiler(".version 6\nCreate()\n{\n\tsleep 1;\n}\n").Compile()
	if err != nil {
		t.Fatal(err)
	}
	equalWords(t, codeWords(kingdoms), push, 1, scripting.OP_SLEEP, alloc, scripting.OP_RETURN)
}

func TestCompileIgnoresTrailingComments(t *testing.T) {
	src := `piece base; // the only piece
Create() // entry point
{ // body
	var x; // scratch
	if (x == 0) // first time
	{
		hide base; // start hidden
	} // end if
	else // otherwise
	{
		show base;
	}
}
`
	cob, err := NewCompiler(src).Compile()
	if err != nil {
		t.Fatal(err)
	}
	equalWords(t, codeWords(cob),
		alloc, local, 0, push, 0, scripting.OP_EQUAL, scripting.OP_JUMP_IF_FALSE, 12,
		scripting.OP_HIDE, 0, scripting.OP_JUMP, 14,
		scripting.OP_SHOW, 0,
		push, 0, scripting.OP_RETURN,
	)
}

func TestCompileHeaderField5(t *testing.T) {
	_, cob := compileBody(t, ".field5 7", "\treturn 0;")
	if cob.UKZero != 7 {
		t.Errorf("UKZero = %d, want 7", cob.UKZero)
	}
}

func TestCompiledCodeIsStackValid(t *testing.T) {
	src := `piece base, turret;
static-var ready;

Aim(heading, pitch)
{
	var i;
	i = 0;
	while (i < 4 AND NOT ready)
	{
		turn turret to y-axis heading speed <90>;
		i = i + 1;
	}
	if (get PIECE_Y(turret) > [10])
	{
		call-script Helper(i, get(4));
	}
	else
	{
		start-script Helper(heading ^ pitch, ~i);
	}
	return (1);
}

Helper(a, b)
{
	ready = rand(a, b) | (a & b);
}
`
	cob, err := NewCompiler(src).Compile()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(cob.NumScripts); i++ {
		insts, err := cob.Disassemble(i)
		if err != nil {
			t.Fatal(err)
		}
		if report := scripting.AnalyzeStack(insts, false); len(report.Issues) != 0 {
			t.Errorf("%s: %+v", cob.ScriptNames[i], report.Issues)
		}
	}
}

func TestCompileModuloOnlyUnderVersion6(t *testing.T) {
	err := compileError(t, "", "\tvar x;\n\tx = 7 % 3;")
	if err == nil || !strings.Contains(err.Error(), "modulo") {
		t.Fatalf("TA script: err = %v, want a modulo error", err)
	}

	// TA: Kingdoms keeps the old encoding, 0x10037000, and a warning.
	c := NewCompiler(".version 6\nCreate()\n{\n\tvar x;\n\tx = 7 % 3;\n\tx = x % 2;\n}\n")
	cob, err := c.Compile()
	if err != nil {
		t.Fatalf(".version 6: %v", err)
	}
	equalWords(t, codeWords(cob),
		alloc,
		push, 7, push, 3, 0x10037000, popLoc, 0,
		local, 0, push, 2, 0x10037000, popLoc, 0,
		alloc, scripting.OP_RETURN,
	)
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "TA: Kingdoms") {
		t.Errorf("warnings = %q, want one about TA: Kingdoms", w)
	}
}
