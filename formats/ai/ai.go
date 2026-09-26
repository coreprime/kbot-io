// Package ai reads Total Annihilation and TA: Kingdoms computer-player
// profiles (the text files in ai/) and evaluates them the way TA 3.1c does.
//
// # Syntax
//
// A profile is read line by line; lines end at '\n'. Each line is split into
// words on space, tab, CR, LF, VT and FF, and '#' ends the line wherever it
// appears, even inside a word. The game keeps at most 20 words per line and
// gives their characters a shared budget of 126 bytes, cutting words that do
// not fit. The first word, compared case-insensitively, is the directive;
// lines starting with any other word are ignored, which is how the "//"
// comment lines of the retail profiles are skipped.
//
//   - plan <word>...: starts a plan. The weight and limit lines that follow
//     apply only when the plan matches the game's difficulty: a word equal to
//     easy, medium or hard (case-insensitive) names that difficulty, and
//     "any" matches every difficulty, but only as the first word. A plan line
//     that matches nothing, including a bare "plan", disables the lines after
//     it until the next plan line.
//   - weight <target> <value>: scales the target's build priority.
//   - limit <target> <value>: caps how many of the target a computer player
//     builds.
//
// Lines before the first plan line (the [AIFile.Preamble]) are ignored when a
// game starts, because no plan has matched yet. The game re-applies the whole
// profile when it is reloaded during a game, and the plan state is then left
// over from the previous pass, so the preamble applies on a reload only when
// the last plan line matched. TA: Kingdoms profiles have no plan lines and
// their directives are always in force; [ParseOptions.DefaultPlan] reads such
// files into one implicit plan that matches every difficulty.
//
// # Values
//
// A value is the longest decimal prefix of the third word. A weight takes an
// optional sign, digits, an optional fraction and an optional exponent;
// hexadecimal, infinity and NaN forms are not numbers. A limit takes an
// optional sign and digits and wraps to 32 bits. A missing or non-numeric
// value reads as 0 and the directive still applies, so "limit CORFORT O"
// forbids CORFORT. [Parse] keeps every such directive and explains what the
// game makes of it in [AIFile.Diagnostics].
//
// # Targets and precedence
//
// A target that names a unit (case-insensitively) applies to that unit alone
// and locks it: later weight lines (for a weight) or limit lines (for a
// limit) no longer change it. Any other target is a category word matched
// against each unit's FBI Category field, or "all", which matches every unit;
// these skip locked units. [Resolver] resolves targets against a unit table
// and computes the settings a profile leaves each unit with.
//
// Every unit starts at a weight of 100% and no limit. A weight multiplies the
// current percentage, truncates it to an integer and clamps it to 0-100, so a
// value above 1 can restore a unit reduced earlier but never raises it past
// 100%. A limit replaces the current one: -1 means unlimited, N lets a
// computer player own at most N, and 0 or any other negative value forbids
// the unit. The resolver models TA 3.1c; TA: Kingdoms uses a different weight
// scale, which it does not model.
package ai

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Difficulty represents a game difficulty level
type Difficulty int

const (
	Easy Difficulty = iota
	Medium
	Hard
)

func (d Difficulty) String() string {
	switch d {
	case Easy:
		return "Easy"
	case Medium:
		return "Medium"
	case Hard:
		return "Hard"
	default:
		return "Unknown"
	}
}

// word returns the plan word that names d.
func (d Difficulty) word() string {
	switch d {
	case Easy:
		return "easy"
	case Medium:
		return "medium"
	case Hard:
		return "hard"
	default:
		return ""
	}
}

// UnitWeight is one weight directive.
type UnitWeight struct {
	// UnitName is the target word upper-cased: a unit name, an FBI category
	// word or ALL (see [Resolver.Kind]). It is empty when the line has no
	// target, which the game applies to nothing.
	UnitName string
	// Weight is the value the game reads (see the package documentation).
	Weight float64
	// RawValue is the value word as written; empty when the line has none.
	RawValue string
	// Line is the 1-based line number of the directive.
	Line int
}

// UnitLimit is one limit directive.
type UnitLimit struct {
	// UnitName is the target word upper-cased: a unit name, an FBI category
	// word or ALL (see [Resolver.Kind]). It is empty when the line has no
	// target, which the game applies to nothing.
	UnitName string
	// Maximum is the value the game reads, always within the 32-bit range:
	// -1 is unlimited, 0 and other negative values forbid the target.
	Maximum int
	// RawValue is the value word as written; empty when the line has none.
	RawValue string
	// Line is the 1-based line number of the directive.
	Line int
}

// Unlimited reports whether the limit lifts the cap (-1).
func (l UnitLimit) Unlimited() bool { return l.Maximum == -1 }

// Forbids reports whether the limit stops the target being built: 0 or any
// negative value other than -1.
func (l UnitLimit) Forbids() bool { return l.Maximum <= 0 && l.Maximum != -1 }

// DifficultyPlan is one plan line and the weight and limit lines that follow
// it up to the next plan line.
type DifficultyPlan struct {
	// Name is the words after "plan", joined by single spaces, with any '#'
	// comment removed (e.g. "easy", "Medium Hard"). It is empty for a bare
	// plan line.
	Name string
	// Words are the words after "plan", lower-cased, as the game compares
	// them.
	Words []string
	// Implicit marks the plan ParseOptions.DefaultPlan creates for lines
	// before the first plan line; it matches every difficulty.
	Implicit bool
	// Line is the 1-based line number of the plan line (0 for an implicit
	// plan).
	Line    int
	Weights []UnitWeight // Weight directives, in file order
	Limits  []UnitLimit  // Limit directives, in file order
}

// Matches reports whether the game applies this plan's directives at
// difficulty d: the first word is "any", or some word names d.
func (p *DifficultyPlan) Matches(d Difficulty) bool {
	if p.Implicit {
		return true
	}
	want := d.word()
	for i, w := range p.Words {
		if (i == 0 && w == "any") || (want != "" && w == want) {
			return true
		}
	}
	return false
}

// Diagnostic explains how the game reads a line that is not written the
// usual way.
type Diagnostic struct {
	Line    int // 1-based line number
	Message string
}

func (d Diagnostic) String() string { return fmt.Sprintf("line %d: %s", d.Line, d.Message) }

// AIFile represents a complete AI configuration file
type AIFile struct {
	// Preamble holds the weight and limit lines before the first plan line,
	// which the game ignores when a game starts. It is nil when there are
	// none, or when ParseOptions.DefaultPlan moved them into an implicit plan.
	Preamble *DifficultyPlan
	// Plans holds the plans in file order.
	Plans []DifficultyPlan
	// Diagnostics lists lines the game reads differently from how they look.
	Diagnostics []Diagnostic
}

// PlansFor returns the plans whose directives the game applies at
// difficulty d, in file order.
func (f *AIFile) PlansFor(d Difficulty) []DifficultyPlan {
	var out []DifficultyPlan
	for i := range f.Plans {
		if f.Plans[i].Matches(d) {
			out = append(out, f.Plans[i])
		}
	}
	return out
}

// DefaultPlanName is the name of the implicit plan ParseOptions.DefaultPlan
// creates for lines before the first plan line.
const DefaultPlanName = "default"

// ParseOptions adjusts how a profile is read.
type ParseOptions struct {
	// DefaultPlan puts the weight and limit lines before the first plan line
	// into an implicit plan named DefaultPlanName that matches every
	// difficulty, instead of AIFile.Preamble. This is how TA: Kingdoms
	// profiles, which have no plan lines, take effect.
	DefaultPlan bool
}

// IsAIFile reports whether content looks like an AI profile: some line's
// first word is plan, weight or limit (case-insensitive) and is followed by
// another word.
func IsAIFile(content []byte) bool {
	found := false
	forEachLine(content, func(_ int, line []byte) bool {
		words, _ := tokenize(line)
		if len(words) >= 2 && directive(words[0]) != "" {
			found = true
			return false
		}
		return true
	})
	return found
}

// Parse parses a profile with the default options. It does not fail: lines
// the game ignores are skipped and unusual lines are kept and reported in
// AIFile.Diagnostics. The error result is always nil.
func Parse(content []byte) (*AIFile, error) {
	return ParseWith(content, ParseOptions{})
}

// ParseWith parses a profile with the given options. See [Parse].
func ParseWith(content []byte, opts ParseOptions) (*AIFile, error) {
	p := &parser{file: &AIFile{}, opts: opts, plan: -1}
	forEachLine(content, func(n int, line []byte) bool {
		p.line(n, line)
		return true
	})
	return p.file, nil
}

type parser struct {
	file *AIFile
	opts ParseOptions
	// plan is the index of the current plan, or -1 before the first plan.
	plan          int
	preambleNoted bool
}

func (p *parser) note(line int, format string, args ...any) {
	p.file.Diagnostics = append(p.file.Diagnostics, Diagnostic{Line: line, Message: fmt.Sprintf(format, args...)})
}

func (p *parser) line(n int, raw []byte) {
	words, cut := tokenize(raw)
	if len(words) == 0 {
		return
	}
	kind := directive(words[0])
	if kind == "" {
		if !strings.HasPrefix(words[0], "//") {
			p.note(n, "unknown directive %q; the game ignores the line", words[0])
		}
		return
	}
	if cut != "" {
		p.note(n, "%s", cut)
	}
	switch kind {
	case "plan":
		p.planLine(n, words[1:])
	case "weight", "limit":
		p.directiveLine(n, kind, words)
	}
}

func (p *parser) planLine(n int, words []string) {
	plan := DifficultyPlan{
		Name:    strings.Join(words, " "),
		Words:   make([]string, len(words)),
		Line:    n,
		Weights: []UnitWeight{},
		Limits:  []UnitLimit{},
	}
	for i, w := range words {
		plan.Words[i] = asciiLower(w)
	}
	if !plan.Matches(Easy) && !plan.Matches(Medium) && !plan.Matches(Hard) {
		p.note(n, "plan names no difficulty; the game disables the lines that follow")
	}
	for i, w := range plan.Words {
		if i > 0 && w == "any" {
			p.note(n, "plan: \"any\" counts only as the first word")
			break
		}
	}
	p.file.Plans = append(p.file.Plans, plan)
	p.plan = len(p.file.Plans) - 1
}

func (p *parser) directiveLine(n int, kind string, words []string) {
	target, value := word(words, 1), word(words, 2)
	name := strings.ToUpper(target)
	if target == "" {
		p.note(n, "%s has no target; the game applies it to nothing", kind)
	}
	if len(words) > 3 && !strings.HasPrefix(words[3], "//") {
		p.note(n, "%s %s: words after the value are ignored (%q)", kind, target, strings.Join(words[3:], " "))
	}

	dst := p.current(n)
	switch kind {
	case "weight":
		v, used := parseWeight(value)
		p.noteValue(n, kind, target, value, used, strconv.FormatFloat(v, 'g', -1, 64))
		dst.Weights = append(dst.Weights, UnitWeight{UnitName: name, Weight: v, RawValue: value, Line: n})
	case "limit":
		v, used := parseLimit(value)
		p.noteValue(n, kind, target, value, used, strconv.Itoa(int(v)))
		if used > 0 {
			if exact, err := strconv.ParseInt(value[:used], 10, 64); err != nil || exact != int64(v) {
				p.note(n, "limit %s: %s is outside the 32-bit range; the game reads %d", target, value[:used], v)
			}
		}
		dst.Limits = append(dst.Limits, UnitLimit{UnitName: name, Maximum: int(v), RawValue: value, Line: n})
	}
}

// noteValue reports a value that is missing or only partly numeric.
func (p *parser) noteValue(n int, kind, target, value string, used int, read string) {
	switch {
	case value == "":
		p.note(n, "%s %s has no value; the game reads 0", kind, target)
	case used == 0:
		p.note(n, "%s %s: %q is not a number; the game reads 0", kind, target, value)
	case used < len(value):
		p.note(n, "%s %s: the game reads %q as %s", kind, target, value, read)
	}
}

// current returns the plan the next directive belongs to, creating the
// preamble or the implicit plan when needed.
func (p *parser) current(n int) *DifficultyPlan {
	if p.plan >= 0 {
		return &p.file.Plans[p.plan]
	}
	if p.opts.DefaultPlan {
		p.file.Plans = append(p.file.Plans, DifficultyPlan{
			Name:     DefaultPlanName,
			Implicit: true,
			Weights:  []UnitWeight{},
			Limits:   []UnitLimit{},
		})
		p.plan = len(p.file.Plans) - 1
		return &p.file.Plans[p.plan]
	}
	if p.file.Preamble == nil {
		p.file.Preamble = &DifficultyPlan{Weights: []UnitWeight{}, Limits: []UnitLimit{}}
	}
	if !p.preambleNoted {
		p.preambleNoted = true
		p.note(n, "directives before the first plan line are ignored when a game starts")
	}
	return p.file.Preamble
}

// forEachLine calls fn for every '\n'-terminated line of content with its
// 1-based number, stopping when fn returns false.
func forEachLine(content []byte, fn func(n int, line []byte) bool) {
	for n, start := 1, 0; start < len(content); n++ {
		end := bytes.IndexByte(content[start:], '\n')
		if end < 0 {
			end = len(content)
		} else {
			end += start
		}
		if !fn(n, content[start:end]) {
			return
		}
		start = end + 1
	}
}

const (
	// maxWords is the number of words the game keeps per line.
	maxWords = 20
	// wordBytes is the character budget the kept words share.
	wordBytes = 126
)

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

// tokenize splits one line into words as the game does. When words are cut
// or dropped, cut describes it.
func tokenize(line []byte) (words []string, cut string) {
	used := 0 // characters and terminators stored so far
	truncated, dropped := false, false
	i := 0
	for i < len(line) {
		for i < len(line) && isSpace(line[i]) {
			i++
		}
		if i == len(line) || line[i] == '#' {
			break
		}
		start := i
		for i < len(line) && !isSpace(line[i]) && line[i] != '#' && used < wordBytes {
			i++
			used++
		}
		kept := string(line[start:i])
		for i < len(line) && !isSpace(line[i]) && line[i] != '#' {
			i++
			truncated = true
		}
		used++
		if len(words) < maxWords {
			words = append(words, kept)
		} else {
			dropped = true
		}
		if used >= wordBytes+maxWords {
			dropped = dropped || hasWord(line[i:])
			break
		}
	}
	switch {
	case truncated && dropped:
		cut = "the line is longer than the game keeps; words are cut and later words ignored"
	case truncated:
		cut = "the line is longer than the game keeps; words are cut"
	case dropped:
		cut = fmt.Sprintf("the game keeps only the first %d words of a line", maxWords)
	}
	return words, cut
}

// hasWord reports whether rest holds another word before any '#'.
func hasWord(rest []byte) bool {
	for _, c := range rest {
		if c == '#' {
			return false
		}
		if !isSpace(c) {
			return true
		}
	}
	return false
}

func word(words []string, i int) string {
	if i < len(words) {
		return words[i]
	}
	return ""
}

// directive returns the lower-case directive a first word selects, or "".
func directive(w string) string {
	switch l := asciiLower(w); l {
	case "plan", "weight", "limit":
		return l
	}
	return ""
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// parseWeight reads the longest decimal prefix of s: optional sign, digits,
// optional fraction, and an exponent only when digits follow the 'e'. It
// returns 0 and 0 when s has no digits before any exponent.
func parseWeight(s string) (float64, int) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && isDigit(s[i]) {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0, 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	// A well-formed prefix that overflows reads as infinity, as in C.
	v, _ := strconv.ParseFloat(s[:i], 64)
	return v, i
}

// parseLimit reads the longest decimal integer prefix of s (optional sign and
// digits), wrapping to 32 bits. It returns 0 and 0 when s has no digits.
func parseLimit(s string) (int32, int) {
	i := 0
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	start := i
	var total uint32
	for i < len(s) && isDigit(s[i]) {
		total = total*10 + uint32(s[i]-'0')
		i++
	}
	if i == start {
		return 0, 0
	}
	if neg {
		total = -total
	}
	return int32(total), i
}

// TargetKind says what a directive's target names.
type TargetKind int

const (
	// TargetCategory is a category word matched against each unit's FBI
	// Category field. Names that match nothing are also this kind.
	TargetCategory TargetKind = iota
	// TargetUnit is the name of a unit; the directive locks that unit.
	TargetUnit
	// TargetAll is "all", matching every unit.
	TargetAll
)

func (k TargetKind) String() string {
	switch k {
	case TargetUnit:
		return "unit"
	case TargetAll:
		return "all"
	default:
		return "category"
	}
}

// Unit describes one unit type to a Resolver.
type Unit struct {
	// Name is the unit name (the FBI UnitName).
	Name string
	// Categories are the words of the unit's FBI Category field.
	Categories []string
}

// Setting is the state a profile leaves a unit in.
type Setting struct {
	// WeightPercent is the build priority percentage, 0-100; 100 leaves the
	// unit's priority unchanged.
	WeightPercent int
	// WeightLocked is set once a weight line names the unit itself.
	WeightLocked bool
	// Limit is the most units of the type a computer player owns: -1 is
	// unlimited, 0 and other negative values forbid it.
	Limit int
	// LimitLocked is set once a limit line names the unit itself.
	LimitLocked bool
}

// Forbidden reports whether the limit stops the unit being built.
func (s Setting) Forbidden() bool { return s.Limit <= 0 && s.Limit != -1 }

// Resolver resolves directive targets against a unit table and applies
// profiles with TA 3.1c's precedence rules.
type Resolver struct {
	units []Unit
}

// NewResolver returns a Resolver for the given unit table.
func NewResolver(units []Unit) *Resolver {
	return &Resolver{units: append([]Unit(nil), units...)}
}

// Kind reports what target names: a unit when some unit has that name
// (case-insensitively), all for "all", and a category otherwise.
func (r *Resolver) Kind(target string) TargetKind {
	if r.isUnit(target) {
		return TargetUnit
	}
	if asciiLower(target) == "all" {
		return TargetAll
	}
	return TargetCategory
}

func (r *Resolver) isUnit(target string) bool {
	for _, u := range r.units {
		if asciiLower(u.Name) == asciiLower(target) {
			return true
		}
	}
	return false
}

// matches reports whether a directive naming target applies to unit u.
func matches(u Unit, target string, exact bool) bool {
	t := asciiLower(target)
	if exact {
		return asciiLower(u.Name) == t
	}
	if t == "all" {
		return true
	}
	for _, c := range u.Categories {
		if c != "" && asciiLower(c) == t {
			return true
		}
	}
	return false
}

// Matches returns the names of the units a directive naming target applies
// to, ignoring locks.
func (r *Resolver) Matches(target string) []string {
	exact := r.isUnit(target)
	var out []string
	for _, u := range r.units {
		if matches(u, target, exact) {
			out = append(out, u.Name)
		}
	}
	return out
}

// Apply returns the settings each unit has after the game applies f at
// difficulty d when a game starts, keyed by Unit.Name: the preamble is
// skipped and each plan's directives apply when the plan matches d.
func (r *Resolver) Apply(f *AIFile, d Difficulty) map[string]Setting {
	state := make([]Setting, len(r.units))
	for i := range state {
		state[i] = Setting{WeightPercent: 100, Limit: -1}
	}
	for pi := range f.Plans {
		plan := &f.Plans[pi]
		if !plan.Matches(d) {
			continue
		}
		for _, w := range plan.Weights {
			r.applyWeight(state, w)
		}
		for _, l := range plan.Limits {
			r.applyLimit(state, l)
		}
	}
	out := make(map[string]Setting, len(r.units))
	for i, u := range r.units {
		out[u.Name] = state[i]
	}
	return out
}

func (r *Resolver) applyWeight(state []Setting, w UnitWeight) {
	exact := r.isUnit(w.UnitName)
	factor := float64(float32(w.Weight))
	for i, u := range r.units {
		if state[i].WeightLocked || !matches(u, w.UnitName, exact) {
			continue
		}
		state[i].WeightPercent = clampPercent(truncateInt32(float64(state[i].WeightPercent) * factor))
		if exact {
			state[i].WeightLocked = true
		}
	}
}

func (r *Resolver) applyLimit(state []Setting, l UnitLimit) {
	exact := r.isUnit(l.UnitName)
	for i, u := range r.units {
		if state[i].LimitLocked || !matches(u, l.UnitName, exact) {
			continue
		}
		state[i].Limit = l.Maximum
		if exact {
			state[i].LimitLocked = true
		}
	}
}

// truncateInt32 truncates v toward zero; values outside the 32-bit range and
// non-finite values give the most negative 32-bit value, as the game's
// conversion does.
func truncateInt32(v float64) int {
	if math.IsNaN(v) || v >= 1<<31 || v < -(1<<31) {
		return math.MinInt32
	}
	return int(int32(v))
}

func clampPercent(v int) int {
	switch {
	case v < 1:
		return 0
	case v < 100:
		return v
	default:
		return 100
	}
}
