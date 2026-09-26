package tsf

import (
	"fmt"
	"strings"
)

// Document is a parsed TSF text file: an optional block of leading lines
// (comments and blank lines), one or more top-level sections, and any trailing
// lines.
//
// Re-serializing a parsed Document reproduces the original bytes. Every line
// is kept verbatim with its own terminator, including indentation, comments,
// blank lines and mixed line endings, for as long as the node it belongs to is
// unchanged. Changed and new nodes are written in the canonical layout: tab
// indentation, "Key = Value;" and LineEnding.
type Document struct {
	// Leading holds verbatim lines that appear before the first section
	// (typically a comment and a blank line).
	Leading []string
	// Sections holds the top-level sections in order.
	Sections []*Section
	// Trailing holds verbatim lines that appear after the last section. When
	// the text ends with a line terminator, the last entry is "".
	Trailing []string
	// LineEnding is the terminator written after new or changed lines: "\r\n"
	// when the parsed text contains one, otherwise "\n".
	LineEnding string

	// leadingEOL and trailingEOL hold the terminator read after each line of
	// Leading and Trailing. They are used only while their lengths still
	// match.
	leadingEOL, trailingEOL []string
}

// Section is a named brace-delimited block. Its body is an ordered list of
// assignments, nested sections and trivia.
type Section struct {
	Name string
	Body []Node

	src *sectionSource
}

// sectionSource records how a parsed section was written.
type sectionSource struct {
	name                string    // Name as parsed
	before              []rawLine // trivia before a top-level section after the first
	header, open, close rawLine
}

// rawLine is one line as read: its text without the terminator, and the
// terminator ("\r\n", "\n", or "" for the last line of the text).
type rawLine struct {
	text, eol string
}

// Node is one element of a section body: an *Assignment, a *Section or a
// *Trivia.
type Node interface{ isNode() }

// Assignment is a "Key = Value;" statement.
type Assignment struct {
	Key   string
	Value string

	src *assignmentSource
}

// assignmentSource records how a parsed assignment was written.
type assignmentSource struct {
	key, value string
	line       rawLine
}

// Trivia is a blank or comment-only line inside a section body, kept so the
// document re-serializes byte for byte. Text is the whole line as written.
type Trivia struct {
	Text string

	eol string
}

func (*Assignment) isNode() {}
func (*Section) isNode()    {}
func (*Trivia) isNode()     {}

// Get returns the value of the first assignment with the given key and whether
// it was found. Section and trivia nodes are ignored.
func (s *Section) Get(key string) (string, bool) {
	for _, n := range s.Body {
		if a, ok := n.(*Assignment); ok && a.Key == key {
			return a.Value, true
		}
	}
	return "", false
}

// Subsections returns the nested sections of s in order.
func (s *Section) Subsections() []*Section {
	var out []*Section
	for _, n := range s.Body {
		if sub, ok := n.(*Section); ok {
			out = append(out, sub)
		}
	}
	return out
}

// ParseTSF parses TSF text into a Document.
//
// A section is a "[Name]" line followed by a "{" line and closed by a "}"
// line. Inside it, each line is a nested section, a "Key = Value;"
// assignment (the value ends at the first ';', which may be omitted at the
// end of the line), or a blank or comment line. "//" comments run to the end
// of the line and "/* */" comments may span lines. A comment starts only
// where no value can be running: at the start of a line, after a section
// header, "{" or "}", or after the ';' that ends a value. Elsewhere "//" and
// "/*" are ordinary text, so "Filename = art//a.png;" has the value
// "art//a.png", and a comment after a value needs the ';' before it.
// Top-level sections may be separated by blank and comment lines.
func ParseTSF(text string) (*Document, error) {
	lines := splitLines(text)
	p := &tsfParser{lines: lines, codes: stripComments(lines)}

	doc := &Document{LineEnding: "\n"}
	if strings.Contains(text, "\r\n") {
		doc.LineEnding = "\r\n"
	}

	// Leading lines: every line up to the first section header.
	i := 0
	for i < len(lines) && !isSectionHeader(p.codes[i]) {
		doc.Leading = append(doc.Leading, lines[i].text)
		doc.leadingEOL = append(doc.leadingEOL, lines[i].eol)
		i++
	}

	// Top-level sections, possibly separated by blank or comment lines.
	for i < len(lines) {
		j := i
		for j < len(lines) && strings.TrimSpace(p.codes[j]) == "" {
			j++
		}
		if j == len(lines) || !isSectionHeader(p.codes[j]) {
			break
		}
		sec, next, err := p.section(j)
		if err != nil {
			return nil, err
		}
		sec.src.before = lines[i:j]
		doc.Sections = append(doc.Sections, sec)
		i = next
	}

	// Anything left over is trailing.
	for ; i < len(lines); i++ {
		doc.Trailing = append(doc.Trailing, lines[i].text)
		doc.trailingEOL = append(doc.trailingEOL, lines[i].eol)
	}

	if len(doc.Sections) == 0 {
		return nil, fmt.Errorf("tsf: no sections found")
	}
	return doc, nil
}

// splitLines splits text at '\n', keeping each line's terminator. A '\r'
// before the '\n' belongs to the terminator.
func splitLines(text string) []rawLine {
	parts := strings.Split(text, "\n")
	lines := make([]rawLine, len(parts))
	for i, s := range parts {
		switch {
		case i == len(parts)-1:
			lines[i] = rawLine{text: s}
		case strings.HasSuffix(s, "\r"):
			lines[i] = rawLine{text: s[:len(s)-1], eol: "\r\n"}
		default:
			lines[i] = rawLine{text: s, eol: "\n"}
		}
	}
	return lines
}

// stripComments returns each line's text with "//" and "/* */" comments
// removed (a block comment becomes a space), carrying block comments across
// lines. Comment markers count only where commentMayStart allows them.
func stripComments(lines []rawLine) []string {
	codes := make([]string, len(lines))
	inBlock := false
	for i, l := range lines {
		s := l.text
		var b strings.Builder
		for k := 0; k < len(s); {
			switch {
			case inBlock:
				end := strings.Index(s[k:], "*/")
				if end < 0 {
					k = len(s)
					continue
				}
				k += end + 2
				inBlock = false
				b.WriteByte(' ')
			case strings.HasPrefix(s[k:], "/*") && commentMayStart(b.String()):
				inBlock = true
				k += 2
			case strings.HasPrefix(s[k:], "//") && commentMayStart(b.String()):
				k = len(s)
			default:
				b.WriteByte(s[k])
				k++
			}
		}
		codes[i] = b.String()
	}
	return codes
}

// commentMayStart reports whether a comment can begin after code, the
// uncommented text read so far on a line: when nothing but white space has
// been read, after a complete section header, "{" or "}", and after the ';'
// that ends a value. Anywhere else a comment marker is part of a name or
// value.
func commentMayStart(code string) bool {
	t := strings.TrimSpace(code)
	return t == "" || t == "{" || t == "}" || isSectionHeader(t) || strings.Contains(t, ";")
}

type tsfParser struct {
	lines []rawLine
	codes []string // line text without comments
}

// section parses the section whose header is at lines[start]. It returns the
// section and the index of the first line after the closing brace.
func (p *tsfParser) section(start int) (*Section, int, error) {
	header := strings.TrimSpace(p.codes[start])
	name := header[1 : len(header)-1]
	sec := &Section{Name: name, src: &sectionSource{name: name, header: p.lines[start]}}

	i := start + 1
	if i >= len(p.lines) || strings.TrimSpace(p.codes[i]) != "{" {
		return nil, 0, fmt.Errorf("tsf: line %d: expected '{' after section [%s]", i+1, name)
	}
	sec.src.open = p.lines[i]
	i++

	for i < len(p.lines) {
		code := strings.TrimSpace(p.codes[i])
		switch {
		case code == "}":
			sec.src.close = p.lines[i]
			return sec, i + 1, nil
		case isSectionHeader(code):
			sub, next, err := p.section(i)
			if err != nil {
				return nil, 0, err
			}
			sec.Body = append(sec.Body, sub)
			i = next
		case code == "":
			sec.Body = append(sec.Body, &Trivia{Text: p.lines[i].text, eol: p.lines[i].eol})
			i++
		default:
			a, err := parseAssignment(code)
			if err != nil {
				return nil, 0, fmt.Errorf("tsf: line %d: section [%s]: %w", i+1, name, err)
			}
			a.src = &assignmentSource{key: a.Key, value: a.Value, line: p.lines[i]}
			sec.Body = append(sec.Body, a)
			i++
		}
	}
	return nil, 0, fmt.Errorf("tsf: unterminated section [%s]", name)
}

func parseAssignment(s string) (*Assignment, error) {
	eq := strings.IndexByte(s, '=')
	if eq < 0 {
		return nil, fmt.Errorf("malformed statement %q", s)
	}
	key := strings.TrimSpace(s[:eq])
	if key == "" {
		return nil, fmt.Errorf("empty key in %q", s)
	}
	value := s[eq+1:]
	if semi := strings.IndexByte(value, ';'); semi >= 0 {
		if rest := strings.TrimSpace(value[semi+1:]); rest != "" {
			return nil, fmt.Errorf("unexpected %q after ';' in %q", rest, s)
		}
		value = value[:semi]
	}
	return &Assignment{Key: key, Value: strings.TrimSpace(value)}, nil
}

func isSectionHeader(code string) bool {
	t := strings.TrimSpace(code)
	return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") && len(t) > 2
}

// lineWriter collects output lines and joins them, writing the default
// terminator where a line has none of its own. The last line gets none.
type lineWriter struct {
	ending string
	lines  []rawLine
}

func (w *lineWriter) add(text, eol string) { w.lines = append(w.lines, rawLine{text: text, eol: eol}) }

func (w *lineWriter) String() string {
	var b strings.Builder
	for i, l := range w.lines {
		b.WriteString(l.text)
		if i == len(w.lines)-1 {
			break
		}
		if l.eol != "" {
			b.WriteString(l.eol)
		} else {
			b.WriteString(w.ending)
		}
	}
	return b.String()
}

// verbatim writes lines with the terminators recorded in eols, when eols still
// matches them.
func (w *lineWriter) verbatim(lines, eols []string) {
	for i, l := range lines {
		eol := ""
		if len(eols) == len(lines) {
			eol = eols[i]
		}
		w.add(l, eol)
	}
}

// String renders the document back to text. For a Document produced by
// ParseTSF the output is byte-identical to the input.
func (d *Document) String() string {
	w := &lineWriter{ending: d.LineEnding}
	if w.ending == "" {
		w.ending = "\r\n"
	}
	w.verbatim(d.Leading, d.leadingEOL)
	for _, sec := range d.Sections {
		if sec.src != nil {
			for _, l := range sec.src.before {
				w.add(l.text, l.eol)
			}
		}
		writeSection(w, sec, 0)
	}
	w.verbatim(d.Trailing, d.trailingEOL)
	return w.String()
}

func writeSection(w *lineWriter, sec *Section, depth int) {
	indent := strings.Repeat("\t", depth)
	src := sec.src
	switch {
	case src != nil && sec.Name == src.name:
		w.add(src.header.text, src.header.eol)
	default:
		w.add(indent+"["+sec.Name+"]", "")
	}
	if src != nil {
		w.add(src.open.text, src.open.eol)
	} else {
		w.add(indent+"{", "")
	}
	childIndent := strings.Repeat("\t", depth+1)
	for _, n := range sec.Body {
		switch node := n.(type) {
		case *Assignment:
			if s := node.src; s != nil && node.Key == s.key && node.Value == s.value {
				w.add(s.line.text, s.line.eol)
			} else {
				w.add(childIndent+node.Key+" = "+node.Value+";", "")
			}
		case *Section:
			writeSection(w, node, depth+1)
		case *Trivia:
			w.add(node.Text, node.eol)
		}
	}
	if src != nil {
		w.add(src.close.text, src.close.eol)
	} else {
		w.add(indent+"}", "")
	}
}
