package ai

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/testutil"
)

func mustParseAI(t *testing.T, content string) *AIFile {
	t.Helper()
	f, err := Parse([]byte(content))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return f
}

// hasDiagnostic reports whether f has a diagnostic on line containing text.
func hasDiagnostic(f *AIFile, line int, text string) bool {
	for _, d := range f.Diagnostics {
		if d.Line == line && strings.Contains(d.Message, text) {
			return true
		}
	}
	return false
}

func TestTokenizeLikeTheGame(t *testing.T) {
	content := "plan easy\n" +
		"weight\tARMPW\t0.5\n" + // tabs separate words
		"WEIGHT ARMSOLAR 0.1#x\n" + // '#' ends the line inside a word
		"Limit ARMMEX 4\r\n" + // CR is whitespace
		"# weight ARMLLT 0.3\n" + // a comment line
		"weight:ARMRAD 0.2\n" // not a directive word
	f := mustParseAI(t, content)
	w := f.Plans[0].Weights
	if len(w) != 2 || w[0].UnitName != "ARMPW" || w[0].Weight != 0.5 || w[1].UnitName != "ARMSOLAR" || w[1].Weight != 0.1 {
		t.Fatalf("weights = %+v", w)
	}
	if w[1].RawValue != "0.1" || w[1].Line != 3 {
		t.Errorf("second weight raw %q line %d, want \"0.1\" line 3", w[1].RawValue, w[1].Line)
	}
	l := f.Plans[0].Limits
	if len(l) != 1 || l[0].UnitName != "ARMMEX" || l[0].Maximum != 4 {
		t.Fatalf("limits = %+v", l)
	}
	if !hasDiagnostic(f, 6, "unknown directive") {
		t.Errorf("no diagnostic for the unknown directive: %v", f.Diagnostics)
	}
}

func TestIsAIFileUsesFirstWord(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"limit\tARMMEX\t4", true},
		{"  Weight ARM 0.2\r\n", true},
		{"// header\nplan easy\n", true},
		{"The weight of a unit sets its speed.\nThere is no limit here.", false},
		{"plan\n", false},
		{"#weight ARM 0.2", false},
	}
	for _, c := range cases {
		if got := IsAIFile([]byte(c.content)); got != c.want {
			t.Errorf("IsAIFile(%q) = %v, want %v", c.content, got, c.want)
		}
	}
}

func TestPlanWordsMatchDifficulty(t *testing.T) {
	f := mustParseAI(t, "Plan Medium Hard # both\n"+
		"weight ARMPW 0.5\n"+
		"plan any\n"+
		"plan easy any\n"+
		"plan\n"+
		"weight ARMSOLAR 0.5\n")
	if len(f.Plans) != 4 {
		t.Fatalf("plans = %d, want 4", len(f.Plans))
	}
	mh, anyPlan, easyAny, bare := f.Plans[0], f.Plans[1], f.Plans[2], f.Plans[3]
	if mh.Name != "Medium Hard" || strings.Join(mh.Words, ",") != "medium,hard" {
		t.Errorf("plan 1 name %q words %v", mh.Name, mh.Words)
	}
	check := func(name string, p DifficultyPlan, easy, medium, hard bool) {
		t.Helper()
		if p.Matches(Easy) != easy || p.Matches(Medium) != medium || p.Matches(Hard) != hard {
			t.Errorf("%s matches easy/medium/hard = %v/%v/%v, want %v/%v/%v", name,
				p.Matches(Easy), p.Matches(Medium), p.Matches(Hard), easy, medium, hard)
		}
	}
	check("Medium Hard", mh, false, true, true)
	check("any", anyPlan, true, true, true)
	check("easy any", easyAny, true, false, false)
	check("bare", bare, false, false, false)
	if !hasDiagnostic(f, 4, "first word") {
		t.Errorf("no diagnostic for a late \"any\": %v", f.Diagnostics)
	}
	// A bare plan line starts an empty plan that disables what follows.
	if bare.Name != "" || len(bare.Weights) != 1 || !hasDiagnostic(f, 5, "disables") {
		t.Errorf("bare plan = %+v, diagnostics %v", bare, f.Diagnostics)
	}
	if got := len(f.PlansFor(Easy)); got != 2 {
		t.Errorf("PlansFor(Easy) = %d plans, want 2", got)
	}
}

func TestValuesAreNumericPrefixes(t *testing.T) {
	content := "plan easy\n" +
		"Limit CORFORT O\n" + // 2
		"limit ARMMEX\n" + // 3
		"limit ARMMEX 5.5\n" + // 4
		"weight ARMSOLAR 0x0.8\n" + // 5
		"weight ARMSOLAR inf\n" + // 6
		"limit ARMMEX 3000000000\n" + // 7
		"weight ARMPW 1e3\n" + // 8
		"weight ARMPW 1e\n" + // 9
		"Limit ARM DECOM 4\n" + // 10
		"limit ARMFLAK -7\n" + // 11
		"weight\n" // 12
	f := mustParseAI(t, content)
	lim := f.Plans[0].Limits
	wantLimits := []struct {
		name string
		max  int
		line int
		diag string
	}{
		{"CORFORT", 0, 2, "not a number"},
		{"ARMMEX", 0, 3, "no value"},
		{"ARMMEX", 5, 4, "as 5"},
		{"ARMMEX", -1294967296, 7, "32-bit"},
		{"ARM", 0, 10, "not a number"},
		{"ARMFLAK", -7, 11, ""},
	}
	if len(lim) != len(wantLimits) {
		t.Fatalf("limits = %+v", lim)
	}
	for i, w := range wantLimits {
		if lim[i].UnitName != w.name || lim[i].Maximum != w.max || lim[i].Line != w.line {
			t.Errorf("limit %d = %+v, want %s %d on line %d", i, lim[i], w.name, w.max, w.line)
		}
		if w.diag != "" && !hasDiagnostic(f, w.line, w.diag) {
			t.Errorf("line %d: no diagnostic containing %q in %v", w.line, w.diag, f.Diagnostics)
		}
	}
	if !hasDiagnostic(f, 10, "words after the value") {
		t.Errorf("no diagnostic for the extra word on line 10")
	}

	wts := f.Plans[0].Weights
	wantWeights := []struct {
		name  string
		value float64
		line  int
	}{
		{"ARMSOLAR", 0, 5},
		{"ARMSOLAR", 0, 6},
		{"ARMPW", 1000, 8},
		{"ARMPW", 1, 9},
		{"", 0, 12},
	}
	if len(wts) != len(wantWeights) {
		t.Fatalf("weights = %+v", wts)
	}
	for i, w := range wantWeights {
		if wts[i].UnitName != w.name || wts[i].Weight != w.value || wts[i].Line != w.line {
			t.Errorf("weight %d = %+v, want %s %v on line %d", i, wts[i], w.name, w.value, w.line)
		}
	}
	if !hasDiagnostic(f, 5, "as 0") || !hasDiagnostic(f, 6, "not a number") ||
		!hasDiagnostic(f, 9, "as 1") || !hasDiagnostic(f, 12, "no target") {
		t.Errorf("weight diagnostics missing: %v", f.Diagnostics)
	}
}

func TestLimitSemantics(t *testing.T) {
	cases := []struct {
		max                 int
		unlimited, forbidds bool
	}{
		{-1, true, false},
		{0, false, true},
		{-2, false, true},
		{math.MinInt32, false, true},
		{5, false, false},
	}
	for _, c := range cases {
		l := UnitLimit{Maximum: c.max}
		if l.Unlimited() != c.unlimited || l.Forbids() != c.forbidds {
			t.Errorf("limit %d: unlimited %v forbids %v", c.max, l.Unlimited(), l.Forbids())
		}
		s := Setting{Limit: c.max}
		if s.Forbidden() != c.forbidds {
			t.Errorf("setting limit %d: forbidden %v", c.max, s.Forbidden())
		}
	}
}

func TestPreambleIsSeparate(t *testing.T) {
	f := mustParseAI(t, "Weight CORMAKR 0.2\nWeight ARMMAKR 0.2\n\n// easy\nplan easy\nWeight ARMPW 0.5\n")
	if f.Preamble == nil || len(f.Preamble.Weights) != 2 || f.Preamble.Weights[1].UnitName != "ARMMAKR" {
		t.Fatalf("preamble = %+v", f.Preamble)
	}
	if len(f.Plans) != 1 || f.Plans[0].Name != "easy" || len(f.Plans[0].Weights) != 1 {
		t.Fatalf("plans = %+v", f.Plans)
	}
	if !hasDiagnostic(f, 1, "ignored when a game starts") {
		t.Errorf("no preamble diagnostic: %v", f.Diagnostics)
	}
}

func TestLongLines(t *testing.T) {
	long := strings.Repeat("x", 100<<10)
	content := "// " + long + "\nplan easy\nweight ARMPW 0.5 " + long + "\nlimit ARMMEX 3\n"
	f := mustParseAI(t, content)
	if len(f.Plans) != 1 || len(f.Plans[0].Weights) != 1 || len(f.Plans[0].Limits) != 1 {
		t.Fatalf("long lines lost directives: %+v", f.Plans)
	}
	if f.Plans[0].Weights[0].Weight != 0.5 {
		t.Errorf("weight = %v, want 0.5", f.Plans[0].Weights[0].Weight)
	}

	// The game's line buffer holds 126 characters: a 150-character target is
	// cut to 119 and leaves no room for the value, which then reads as 0.
	f = mustParseAI(t, "plan easy\nweight "+strings.Repeat("A", 150)+" 0.5\n")
	w := f.Plans[0].Weights[0]
	if len(w.UnitName) != 119 || w.Weight != 0 || w.RawValue != "" {
		t.Errorf("weight = %d-character target, value %v %q", len(w.UnitName), w.Weight, w.RawValue)
	}
	if !hasDiagnostic(f, 2, "longer than the game keeps") {
		t.Errorf("no truncation diagnostic: %v", f.Diagnostics)
	}
}

func TestResolverAppliesPrecedence(t *testing.T) {
	r := NewResolver([]Unit{
		{Name: "ARMPW", Categories: []string{"ARM", "LEVEL1", "KBOT"}},
		{Name: "ARMSOLAR", Categories: []string{"ARM", "ENERGY"}},
		{Name: "CORSOLAR", Categories: []string{"CORE", "ENERGY"}},
		{Name: "CORMEX", Categories: []string{"CORE"}},
	})
	if r.Kind("ARM") != TargetCategory || r.Kind("armpw") != TargetUnit || r.Kind("All") != TargetAll {
		t.Errorf("kinds: ARM %v, armpw %v, All %v", r.Kind("ARM"), r.Kind("armpw"), r.Kind("All"))
	}
	if got := strings.Join(r.Matches("energy"), ","); got != "ARMSOLAR,CORSOLAR" {
		t.Errorf("Matches(energy) = %s", got)
	}

	f := mustParseAI(t, `Weight ARMSOLAR 0.1
plan easy
Weight ARM 0.5
Weight ARMPW 0.2
Weight ARMPW 1.0
Weight ARM 2.0
Weight CORSOLAR 1e10
Limit ALL 5
Limit ARMPW -1
Limit ALL 3
Limit CORMEX DECOM
plan hard
Weight ALL 0
`)
	got := r.Apply(f, Easy)
	want := map[string]Setting{
		// 100 × 0.5 × 0.2, then locked against the later lines.
		"ARMPW": {WeightPercent: 10, WeightLocked: true, Limit: -1, LimitLocked: true},
		// The preamble's 0.1 is ignored; 100 × 0.5 × 2.0 is clamped to 100.
		"ARMSOLAR": {WeightPercent: 100, Limit: 3},
		// A percentage that overflows 32 bits becomes 0.
		"CORSOLAR": {WeightPercent: 0, WeightLocked: true, Limit: 3},
		"CORMEX":   {WeightPercent: 100, Limit: 0, LimitLocked: true},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %+v, want %+v", name, got[name], w)
		}
	}
	if !got["CORMEX"].Forbidden() {
		t.Error("CORMEX not forbidden")
	}

	hard := r.Apply(f, Hard)
	if hard["ARMPW"].WeightPercent != 0 || hard["ARMSOLAR"].Limit != -1 {
		t.Errorf("hard settings = %+v", hard)
	}
}

// TestRetailProfiles checks the retail TA profiles: three plans each, one per
// difficulty, and the known oddities reported.
func TestRetailProfiles(t *testing.T) {
	dir := testutil.UnpackedDir(t, "ai")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		n++
		if !IsAIFile(data) {
			t.Errorf("%s: not detected as a profile", e.Name())
		}
		f, _ := Parse(data)
		for _, d := range []Difficulty{Easy, Medium, Hard} {
			if got := len(f.PlansFor(d)); got != 1 {
				t.Errorf("%s: %d plans for %v, want 1", e.Name(), got, d)
			}
		}
		if strings.EqualFold(e.Name(), "krogoth.txt") {
			if f.Preamble == nil || len(f.Preamble.Weights) != 2 {
				t.Errorf("krogoth.txt preamble = %+v, want 2 weights", f.Preamble)
			}
			var fort int
			for _, l := range f.Plans[0].Limits {
				if l.UnitName == "CORFORT" {
					fort++
					if !l.Forbids() || l.RawValue != "O" {
						t.Errorf("krogoth.txt CORFORT limit = %+v, want a forbidding 0 read from \"O\"", l)
					}
				}
			}
			if fort != 1 {
				t.Errorf("krogoth.txt easy plan has %d CORFORT limits, want 1", fort)
			}
		}
	}
	if n == 0 {
		t.Fatal("no profiles found")
	}
}

// TestKingdomsProfiles reads the TA: Kingdoms profiles with DefaultPlan: one
// implicit plan each and nothing the game would read differently.
func TestKingdomsProfiles(t *testing.T) {
	dir := testutil.TAKUnpackedDir(t, "ai")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		f, _ := ParseWith(data, ParseOptions{DefaultPlan: true})
		if len(f.Plans) != 1 || !f.Plans[0].Implicit || f.Preamble != nil {
			t.Errorf("%s: plans %d, preamble %v", e.Name(), len(f.Plans), f.Preamble != nil)
			continue
		}
		if len(f.Diagnostics) != 0 {
			t.Errorf("%s: diagnostics %v", e.Name(), f.Diagnostics)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no profiles found")
	}
}
