package tdf

import (
	"fmt"
	"sort"
	"strings"
)

// Default limits applied when ParseOptions leaves MaxBytes or MaxDepth at 0.
// The game has no limits of its own; these bound memory and recursion for
// hostile input while staying far above anything the game ships.
const (
	DefaultMaxBytes int64 = 16 << 20 // 16 MiB of text
	DefaultMaxDepth       = 64       // nested [section] levels
)

// ParseOptions controls how TDF text is read.
//
// The zero value reads the way the game does wherever the game accepts the
// text, and repairs what the game would refuse (recording a Diagnostic for each
// repair) so that damaged files still load. Set Strict to refuse such text
// instead.
type ParseOptions struct {
	// Strict makes every condition the game refuses a file for a
	// *SyntaxError: a section header with no '{', text with no '=' or a value
	// with no ';' before the end of the text, the end of the text inside a
	// section, and empty input. Conditions the game accepts, such as a stray
	// '}' outside any section, are still accepted and only reported.
	Strict bool

	// MaxBytes caps the text read. 0 means DefaultMaxBytes and a negative
	// value means no limit. Exceeding it is always an error.
	MaxBytes int64

	// MaxDepth caps [section] nesting. 0 means DefaultMaxDepth and a negative
	// value means no limit. Exceeding it is always an error.
	MaxDepth int

	// SkipStrayText drops text that reaches ';', '{', '}' or '[' before any
	// '=' (such as a line holding only ';') instead of reading it as the
	// start of the next key. TA 3.1c reads it as part of the key, so the
	// field that follows is lost to lookups; this option keeps that field,
	// as versions of this package before the game's grammar was adopted did.
	// It suits data whose own reader is known to skip such text. Whether
	// TA: Kingdoms does is not established; three of its retail files
	// (features/zhon/zonruin.tdf, translate/customkeys.tdf and
	// translate/messages.tdf) read differently with it.
	SkipStrayText bool

	// OnDiagnostic, when set, is called for every Diagnostic as it is
	// found. The reader looks ahead, so calls are not strictly in offset
	// order; Diagnose and Document.Diagnostics sort them.
	OnDiagnostic func(Diagnostic)
}

func (o ParseOptions) maxBytes() int64 {
	switch {
	case o.MaxBytes == 0:
		return DefaultMaxBytes
	case o.MaxBytes < 0:
		return 0
	}
	return o.MaxBytes
}

func (o ParseOptions) maxDepth() int {
	switch {
	case o.MaxDepth == 0:
		return DefaultMaxDepth
	case o.MaxDepth < 0:
		return 0
	}
	return o.MaxDepth
}

// DiagKind classifies a Diagnostic.
type DiagKind uint8

// Conditions the game refuses a whole file for. In the default mode they are
// repaired and reported; with ParseOptions.Strict they are errors.
const (
	// DiagMissingBrace: a [section] header is not followed by '{'. Repair:
	// the section is empty and reading resumes after the header.
	DiagMissingBrace DiagKind = iota + 1
	// DiagMissingEquals: text with no '=' anywhere before the end of the
	// text. Repair: the text is ignored.
	DiagMissingEquals
	// DiagMissingSemicolon: a value with no ';' anywhere before the end of
	// the text. Repair: the value runs to the end of the text.
	DiagMissingSemicolon
	// DiagUnexpectedEnd: the text ends inside a section. Repair: the open
	// sections are closed.
	DiagUnexpectedEnd
	// DiagEmptyInput: the input has no bytes at all. The game's file loader
	// refuses zero-length files. Repair: an empty document.
	DiagEmptyInput
)

// Conditions that are errors in every mode.
const (
	// DiagMissingBracket: a '[' has no ']' anywhere after it.
	DiagMissingBracket DiagKind = iota + 32
	// DiagTooDeep: sections are nested deeper than ParseOptions.MaxDepth.
	DiagTooDeep
	// DiagTooLarge: the text is longer than ParseOptions.MaxBytes.
	DiagTooLarge
)

// Conditions the game accepts but that usually mean the text does not say
// what its author intended. They are only ever reported.
const (
	// DiagStrayBrace: a '}' outside any section. The game stops reading
	// there and ignores the rest of the text, and so does this package.
	DiagStrayBrace DiagKind = iota + 64
	// DiagDuplicateSection: a sibling section with the same name
	// (ignoring ASCII case) as an earlier one. Lookups by name find the
	// first; readers that walk sections by position see both.
	DiagDuplicateSection
	// DiagDuplicateKey: a key assigned again in the same section (ignoring
	// ASCII case). The last assignment wins.
	DiagDuplicateKey
	// DiagSuspiciousKey: a key containing ';', '{', '}', '[', ']' or a
	// control character, usually stray text glued onto the next key.
	DiagSuspiciousKey
	// DiagValueLineBreak: a value spanning a line break, usually because
	// the ';' ending it is missing and it swallowed the next line.
	DiagValueLineBreak
	// DiagValueBrace: a value containing '{' or '}', usually because a
	// missing ';' ran it past a section boundary.
	DiagValueBrace
	// DiagUnterminatedComment: a "/*" with no closing "*/". Everything to
	// the end of the text is blanked except its last byte, which the game
	// keeps and reads as text.
	DiagUnterminatedComment
	// DiagEmbeddedNUL: a NUL byte. The text ends at the first NUL.
	DiagEmbeddedNUL
	// DiagLeadingBOM: the input starts with a UTF-8 byte order mark. The
	// game does not skip it; its bytes become part of the first statement.
	DiagLeadingBOM
	// DiagStrayText: text with no '=' before ';', '{', '}' or '[' that
	// ParseOptions.SkipStrayText dropped.
	DiagStrayText
)

var diagText = map[DiagKind]string{
	DiagMissingBrace:        "section header not followed by '{'",
	DiagMissingEquals:       "text with no '=' before the end of the file",
	DiagMissingSemicolon:    "value with no ';' before the end of the file",
	DiagUnexpectedEnd:       "end of file inside a section",
	DiagEmptyInput:          "empty input",
	DiagMissingBracket:      "section header with no closing ']'",
	DiagTooDeep:             "sections nested too deeply",
	DiagTooLarge:            "text too large",
	DiagStrayBrace:          "'}' outside any section ends the text; the rest is ignored",
	DiagDuplicateSection:    "duplicate section name; lookups use the first",
	DiagDuplicateKey:        "key assigned more than once; the last value wins",
	DiagSuspiciousKey:       "key contains stray punctuation or control characters",
	DiagValueLineBreak:      "value spans a line break (missing ';'?)",
	DiagValueBrace:          "value contains '{' or '}' (missing ';'?)",
	DiagUnterminatedComment: "unterminated /* comment; the last byte of the file is kept",
	DiagEmbeddedNUL:         "NUL byte ends the text",
	DiagLeadingBOM:          "UTF-8 byte order mark is read as text",
	DiagStrayText:           "text with no '=' dropped",
}

// String returns a short English description of the kind.
func (k DiagKind) String() string {
	if s, ok := diagText[k]; ok {
		return s
	}
	return fmt.Sprintf("DiagKind(%d)", uint8(k))
}

// Rejected reports whether the game refuses a file containing this condition.
// The size and depth limits are this package's own, so they are not.
func (k DiagKind) Rejected() bool {
	return k >= DiagMissingBrace && k <= DiagEmptyInput || k == DiagMissingBracket
}

// Diagnostic describes one notable condition found in TDF text.
type Diagnostic struct {
	Kind DiagKind
	// Offset is the byte offset into the input where the condition was found.
	Offset int64
	// Section is the path of the enclosing section, names joined by '/';
	// empty at the top level.
	Section string
	// Key is the field key or section name concerned, when there is one.
	Key string
}

// String formats the diagnostic as "offset N [section]: description (key)".
func (d Diagnostic) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "offset %d", d.Offset)
	if d.Section != "" {
		fmt.Fprintf(&b, " in [%s]", d.Section)
	}
	b.WriteString(": ")
	b.WriteString(d.Kind.String())
	if d.Key != "" {
		fmt.Fprintf(&b, " (%q)", d.Key)
	}
	return b.String()
}

// SyntaxError is the error returned for text that cannot be read: a condition
// that is an error in every mode, or, with ParseOptions.Strict, one the game
// refuses.
type SyntaxError struct {
	Diagnostic
}

func (e *SyntaxError) Error() string { return "tdf: " + e.String() }

// Diagnose reads data as Unmarshal and Parse do and returns every Diagnostic
// found, in offset order, including one for any error that stopped the read.
func Diagnose(data []byte) []Diagnostic {
	var out []Diagnostic
	_, err := parseDocumentWith(data, ParseOptions{OnDiagnostic: func(d Diagnostic) { out = append(out, d) }})
	if se, ok := err.(*SyntaxError); ok {
		out = append(out, se.Diagnostic)
	}
	sortDiagnostics(out)
	return out
}

func sortDiagnostics(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Offset < ds[j].Offset })
}
