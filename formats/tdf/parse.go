package tdf

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// This file is the one TDF tokenizer behind Unmarshal, Decoder, Parse,
// Canonicalize, SemanticEqual and Diagnose. It follows the game's reader:
//
//   - The text ends at the end of the input or at the first NUL byte.
//   - Comments are blanked to spaces byte for byte before anything else is
//     read: "//" to the end of the line (the line break survives) and "/*" to
//     the next "*/". An unterminated "/*" blanks everything except the last
//     byte of the text, which the game keeps.
//   - Only space, tab, CR and LF separate tokens.
//   - A statement starting with '[' is a section: the name runs to the next
//     ']' and must be followed by '{'. A statement starting with '}' closes
//     the current section; outside any section it ends the text.
//   - Anything else is a field: the key runs to the next '=' and the value to
//     the next ';', wherever they are, across line breaks, braces and
//     brackets. Key, value and section name are trimmed of separators only.

// element is one parsed statement: a key=value field or a [name]{ ... }
// section holding more elements. Order and duplicates are kept as written.
type element struct {
	key      string     // field key, or section name
	value    string     // trimmed field value (empty for sections)
	section  bool       // true for a [name]{ ... } block
	children []*element // a section's statements

	// Byte offsets into the input. off is where the statement starts (its
	// '[' or the first byte of its key text) and end is just past it (past
	// the ';' or '}'). For a field, vOff:vEnd is the trimmed value. For a
	// section, vOff is just past its '{' (-1 when the header had no body) and
	// vEnd is the offset of its closing '}' (-1 when there is none).
	off, end   int64
	vOff, vEnd int64
}

const (
	stNormal uint8 = iota
	stLine
	stBlock
)

// blanker yields the input with comments blanked to spaces, one output byte
// per input byte, so output offsets are input offsets. It stops at the end of
// the input, at the first NUL byte, at an I/O error or past the byte limit.
type blanker struct {
	r      *bufio.Reader
	state  uint8
	queued bool // a blank is owed for the second byte of "/*" or "*/"
	ended  bool
	err    error
	raw    int64 // offset of the next input byte
	limit  int64 // maximum input bytes; 0 for none

	commentOff  int64 // offset of the "/*" being blanked
	onNUL       func(off int64)
	onOpenBlock func(off int64)
}

// readRaw returns the next input byte, or false at the end of the text.
func (b *blanker) readRaw() (byte, bool) {
	if b.ended {
		return 0, false
	}
	c, err := b.r.ReadByte()
	if err != nil {
		b.stop()
		if err != io.EOF {
			b.err = err
		}
		return 0, false
	}
	if c == 0 {
		b.stop()
		if b.onNUL != nil {
			b.onNUL(b.raw)
		}
		return 0, false
	}
	if b.limit > 0 && b.raw >= b.limit {
		b.stop()
		b.err = &SyntaxError{Diagnostic{Kind: DiagTooLarge, Offset: b.raw}}
		return 0, false
	}
	b.raw++
	return c, true
}

// stop marks the end of the text, reporting an open block comment.
func (b *blanker) stop() {
	if !b.ended && b.state == stBlock && b.onOpenBlock != nil {
		b.onOpenBlock(b.commentOff)
	}
	b.ended = true
}

// peekRaw returns the next input byte without consuming it, or false when the
// text ends there.
func (b *blanker) peekRaw() (byte, bool) {
	if b.ended {
		return 0, false
	}
	p, err := b.r.Peek(1)
	if err != nil || p[0] == 0 || (b.limit > 0 && b.raw >= b.limit) {
		return 0, false
	}
	return p[0], true
}

// next returns the next blanked byte, or false at the end of the text.
func (b *blanker) next() (byte, bool) {
	if b.queued {
		b.queued = false
		return ' ', true
	}
	c, ok := b.readRaw()
	if !ok {
		return 0, false
	}
	switch b.state {
	case stNormal:
		if c != '/' {
			return c, true
		}
		switch n, _ := b.peekRaw(); n {
		case '/':
			b.state = stLine
			return ' ', true
		case '*':
			b.commentOff = b.raw - 1
			b.readRaw()
			b.state = stBlock
			b.queued = true
			return ' ', true
		}
		return c, true
	case stLine:
		if c == '\n' {
			b.state = stNormal
			return c, true
		}
		return ' ', true
	default: // stBlock
		n, ok := b.peekRaw()
		if !ok {
			// The last byte of the text inside an unterminated block
			// comment survives, as it does in the game.
			if b.onOpenBlock != nil {
				b.onOpenBlock(b.commentOff)
			}
			b.state = stNormal
			return c, true
		}
		if c == '*' && n == '/' {
			b.readRaw()
			b.state = stNormal
			b.queued = true
		}
		return ' ', true
	}
}

// parser reads statements from a blanker.
type parser struct {
	b        *blanker
	buf      []byte // blanked text read ahead; buf[pos] is the current byte
	pos      int
	base     int64 // offset of buf[0]
	chunk    []byte
	scratch  []byte
	strict   bool
	skip     bool // ParseOptions.SkipStrayText
	maxDepth int
	onDiag   func(Diagnostic)
	path     []string
	stopped  bool  // a stray '}' ended the text
	stopOff  int64 // where reading stopped: a stray '}' or the end of the text
	rootSecs map[string]bool
	rootKeys map[string]bool
}

// newParser prepares a parser over r and reports input-level diagnostics
// (empty input, a leading byte order mark).
func newParser(r io.Reader, opts ParseOptions) (*parser, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	p := &parser{
		strict:   opts.Strict,
		skip:     opts.SkipStrayText,
		maxDepth: opts.maxDepth(),
		onDiag:   opts.OnDiagnostic,
		rootSecs: map[string]bool{},
		rootKeys: map[string]bool{},
	}
	p.b = &blanker{
		r:           br,
		limit:       opts.maxBytes(),
		onNUL:       func(off int64) { p.note(DiagEmbeddedNUL, off, "") },
		onOpenBlock: func(off int64) { p.note(DiagUnterminatedComment, off, "") },
	}
	head, err := br.Peek(3)
	switch {
	case len(head) == 0 && err == io.EOF:
		if err := p.reject(DiagEmptyInput, 0, ""); err != nil {
			return nil, err
		}
	case len(head) == 0 && err != nil:
		return nil, err
	case bytes.HasPrefix(head, []byte{0xEF, 0xBB, 0xBF}):
		p.note(DiagLeadingBOM, 0, "")
	}
	return p, nil
}

// peek returns the current byte, or false at the end of the text.
func (p *parser) peek() (byte, bool) {
	if p.pos < len(p.buf) {
		return p.buf[p.pos], true
	}
	return p.more()
}

// more reads the next chunk of blanked text.
func (p *parser) more() (byte, bool) {
	p.base += int64(len(p.buf))
	if p.chunk == nil {
		p.chunk = make([]byte, 4096)
	}
	n := 0
	for n < len(p.chunk) {
		c, ok := p.b.next()
		if !ok {
			break
		}
		p.chunk[n] = c
		n++
	}
	p.buf, p.pos = p.chunk[:n], 0
	if n == 0 {
		return 0, false
	}
	return p.buf[0], true
}

// advance consumes the current byte.
func (p *parser) advance() { p.pos++ }

// at is the offset of the current byte (the end of the text once peek has
// reported it).
func (p *parser) at() int64 { return p.base + int64(p.pos) }

func (p *parser) skipSeparators() {
	for {
		c, ok := p.peek()
		if !ok || !isSeparator(c) {
			return
		}
		p.advance()
	}
}

func (p *parser) diag(kind DiagKind, off int64, key string) Diagnostic {
	return Diagnostic{Kind: kind, Offset: off, Section: strings.Join(p.path, "/"), Key: key}
}

// note reports a diagnostic.
func (p *parser) note(kind DiagKind, off int64, key string) {
	if p.onDiag != nil {
		p.onDiag(p.diag(kind, off, key))
	}
}

// reject reports a condition the game refuses: an error in strict mode, a
// diagnostic otherwise.
func (p *parser) reject(kind DiagKind, off int64, key string) error {
	if p.strict {
		return &SyntaxError{p.diag(kind, off, key)}
	}
	p.note(kind, off, key)
	return nil
}

func (p *parser) fail(kind DiagKind, off int64, key string) error {
	return &SyntaxError{p.diag(kind, off, key)}
}

// next returns the next top-level statement, or nil at the end of the text.
func (p *parser) next() (*element, error) {
	for !p.stopped {
		p.skipSeparators()
		c, ok := p.peek()
		if !ok {
			p.stopOff = p.at()
			return nil, p.b.err
		}
		switch c {
		case '[':
			el, err := p.section(1)
			if err != nil {
				return nil, err
			}
			p.noteDuplicate(p.rootSecs, el)
			return el, nil
		case '}':
			p.note(DiagStrayBrace, p.at(), "")
			p.stopped, p.stopOff = true, p.at()
			return nil, nil
		default:
			el, err := p.field()
			if err != nil {
				return nil, err
			}
			if el == nil {
				continue
			}
			p.noteDuplicate(p.rootKeys, el)
			return el, nil
		}
	}
	return nil, nil
}

// all reads every remaining top-level statement.
func (p *parser) all() ([]*element, error) {
	var out []*element
	for {
		el, err := p.next()
		if err != nil {
			return nil, err
		}
		if el == nil {
			return out, nil
		}
		out = append(out, el)
	}
}

func (p *parser) noteDuplicate(seen map[string]bool, el *element) {
	k := foldKey(el.key)
	if seen[k] {
		kind := DiagDuplicateKey
		if el.section {
			kind = DiagDuplicateSection
		}
		p.note(kind, el.off, el.key)
	}
	seen[k] = true
}

// section reads "[name] { ... }" at the current '['. depth is the nesting
// level of its body (1 for a top-level section).
func (p *parser) section(depth int) (*element, error) {
	el := &element{section: true, off: p.at(), vEnd: -1}
	p.advance()
	name := p.scratch[:0]
	for {
		c, ok := p.peek()
		if !ok {
			if p.b.err != nil {
				return nil, p.b.err
			}
			return nil, p.fail(DiagMissingBracket, el.off, "")
		}
		p.advance()
		if c == ']' {
			break
		}
		name = append(name, c)
	}
	el.key = string(trimSeparatorBytes(name))
	p.scratch = name[:0]
	el.end = p.at()
	p.skipSeparators()
	c, ok := p.peek()
	if !ok || c != '{' {
		if p.b.err != nil {
			return nil, p.b.err
		}
		if err := p.reject(DiagMissingBrace, p.at(), el.key); err != nil {
			return nil, err
		}
		el.vOff = -1
		return el, nil
	}
	if p.maxDepth > 0 && depth > p.maxDepth {
		return nil, p.fail(DiagTooDeep, p.at(), el.key)
	}
	p.advance()
	el.vOff = p.at()
	p.path = append(p.path, el.key)
	err := p.body(el, depth)
	p.path = p.path[:len(p.path)-1]
	if err != nil {
		return nil, err
	}
	return el, nil
}

// body reads a section's statements up to its closing '}'.
func (p *parser) body(el *element, depth int) error {
	secs := map[string]bool{}
	keys := map[string]bool{}
	for {
		p.skipSeparators()
		c, ok := p.peek()
		if !ok {
			if p.b.err != nil {
				return p.b.err
			}
			el.end = p.at()
			return p.reject(DiagUnexpectedEnd, p.at(), "")
		}
		switch c {
		case '}':
			el.vEnd = p.at()
			p.advance()
			el.end = p.at()
			return nil
		case '[':
			child, err := p.section(depth + 1)
			if err != nil {
				return err
			}
			p.noteDuplicate(secs, child)
			el.children = append(el.children, child)
		default:
			child, err := p.field()
			if err != nil {
				return err
			}
			if child == nil {
				continue
			}
			p.noteDuplicate(keys, child)
			el.children = append(el.children, child)
		}
	}
}

// field reads "key = value ;" from the current byte. It returns nil when the
// text ends before any '=' (after reporting it), or when SkipStrayText drops
// the text.
func (p *parser) field() (*element, error) {
	el := &element{off: p.at()}
	key := p.scratch[:0]
	for {
		c, ok := p.peek()
		if !ok {
			if p.b.err != nil {
				return nil, p.b.err
			}
			return nil, p.reject(DiagMissingEquals, el.off, "")
		}
		if p.skip && (c == ';' || c == '{' || c == '}' || c == '[') {
			// Drop the text read so far. A delimiter that starts the
			// statement is consumed; any other is left for the caller.
			if len(key) == 0 {
				p.advance()
			}
			p.note(DiagStrayText, el.off, string(trimSeparatorBytes(key)))
			return nil, nil
		}
		p.advance()
		if c == '=' {
			break
		}
		key = append(key, c)
	}
	el.key = string(trimSeparatorBytes(key))
	start := p.at()
	val := key[:0]
	for {
		c, ok := p.peek()
		if !ok {
			if p.b.err != nil {
				return nil, p.b.err
			}
			if err := p.reject(DiagMissingSemicolon, start, el.key); err != nil {
				return nil, err
			}
			break
		}
		p.advance()
		if c == ';' {
			break
		}
		val = append(val, c)
	}
	p.scratch = val[:0]
	el.end = p.at()
	lead := 0
	for lead < len(val) && isSeparator(val[lead]) {
		lead++
	}
	trail := len(val)
	for trail > lead && isSeparator(val[trail-1]) {
		trail--
	}
	el.value = string(val[lead:trail])
	el.vOff, el.vEnd = start+int64(lead), start+int64(trail)

	if suspiciousKey(el.key) {
		p.note(DiagSuspiciousKey, el.off, el.key)
	}
	if strings.ContainsAny(el.value, "\r\n") {
		p.note(DiagValueLineBreak, el.vOff, el.key)
	}
	if strings.ContainsAny(el.value, "{}") {
		p.note(DiagValueBrace, el.vOff, el.key)
	}
	return el, nil
}

func suspiciousKey(k string) bool {
	for i := 0; i < len(k); i++ {
		switch c := k[i]; {
		case c == ';', c == '{', c == '}', c == '[', c == ']':
			return true
		case c < 0x20 && c != '\t', c == 0x7F:
			return true
		}
	}
	return false
}

// parseDocument reads data with the default options.
func parseDocument(data []byte) ([]*element, error) {
	return parseDocumentWith(data, ParseOptions{})
}

// parseDocumentWith reads every top-level statement of data.
func parseDocumentWith(data []byte, opts ParseOptions) ([]*element, error) {
	p, err := newParser(bytes.NewReader(data), opts)
	if err != nil {
		return nil, err
	}
	return p.all()
}
