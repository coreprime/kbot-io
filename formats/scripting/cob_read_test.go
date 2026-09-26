package scripting

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// cobBytes assembles a COB image from its 11 header words and the bytes
// that follow the header.
func cobBytes(header [11]uint32, body ...[]byte) []byte {
	var buf bytes.Buffer
	for _, v := range header {
		_ = binary.Write(&buf, binary.LittleEndian, v)
	}
	for _, b := range body {
		buf.Write(b)
	}
	return buf.Bytes()
}

// minimalCOB is a well-formed one-script, one-piece version-4 file:
// header · code (RETURN) · entry · script-name offset · piece-name offset · "Create\0base\0".
func minimalCOB() []byte {
	const code, entry, sn, pn, pool = 44, 48, 52, 56, 60
	return cobBytes([11]uint32{4, 1, 1, 1, 0, 0, entry, sn, pn, code, pool},
		words(OP_RETURN), words(0), words(pool), words(pool+7), []byte("Create\x00base\x00"))
}

func TestLoadMinimalCOB(t *testing.T) {
	cob, err := LoadFromReader(bytes.NewReader(minimalCOB()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cob.Warnings) != 0 {
		t.Errorf("warnings on a well-formed file: %v", cob.Warnings)
	}
	if cob.ScriptNames[0] != "Create" || cob.PieceNames[0] != "base" || len(cob.Code) != 4 {
		t.Errorf("got scripts %v pieces %v code %d bytes", cob.ScriptNames, cob.PieceNames, len(cob.Code))
	}
}

func TestLoadRejectsTablesPastTheEnd(t *testing.T) {
	// A 60-byte file: code at 44, room for one entry at 48, one script
	// name offset at 52 and one piece name offset at 56.
	tests := map[string][11]uint32{
		// Index-table offset near 2^31: used to panic slicing the code.
		"entry table far past EOF": {4, 1, 0, 1, 0, 0, 0x7FFFFFFF, 52, 56, 44, 60},
		// 0xFFFFFFFC + 4 wraps to 0 in 32 bits.
		"entry table offset wraps": {4, 1, 0, 1, 0, 0, 0xFFFFFFFC, 52, 56, 44, 60},
		"script name table wraps":  {4, 1, 0, 1, 0, 0, 48, 0xFFFFFFFC, 56, 44, 60},
		"piece table past EOF":     {4, 1, 1, 1, 0, 0, 48, 52, 0xFFFFFFF0, 44, 60},
		// Counts that would ask for gigabytes before any bounds check.
		"huge piece count":  {4, 1, 0xFFFFFFFF, 1, 0, 0, 48, 52, 56, 44, 60},
		"huge script count": {4, 0x40000000, 1, 1, 0, 0, 48, 52, 56, 44, 60},
		"code past EOF":     {4, 1, 1, 1, 0, 0, 48, 52, 56, 0x7FFFFFFF, 60},
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			data := cobBytes(header, words(OP_RETURN, 0, 60, 60))
			if _, err := LoadFromReader(bytes.NewReader(data)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadRejectsSoundTableCountPastTheEnd(t *testing.T) {
	// Version 6 with a sound-name count far larger than the file.
	data := cobBytes([11]uint32{6, 0, 0, 1, 0, 0, 56, 56, 56, 52, 56}, words(56, 0x10000000), words(OP_RETURN))
	if _, err := LoadFromReader(bytes.NewReader(data)); err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoadAcceptsEmptyCode(t *testing.T) {
	// What the compiler writes for a BOS with only `piece base;`: the code
	// and every table start at 44.
	data := cobBytes([11]uint32{4, 0, 1, 0, 0, 0, 44, 44, 44, 44, 48}, words(48), []byte("base\x00"))
	cob, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cob.Code) != 0 || cob.PieceNames[0] != "base" {
		t.Errorf("code %d bytes, pieces %v", len(cob.Code), cob.PieceNames)
	}

	var buf bytes.Buffer
	if err := cob.WriteToWriter(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("rewrite differs:\n got %x\nwant %x", buf.Bytes(), data)
	}
}

func TestLoadAcceptsTablesBeforeCode(t *testing.T) {
	// header · entry · script-name offset · "Create\0" · code at the end.
	const entry, sn, pool, code = 44, 48, 52, 60
	data := cobBytes([11]uint32{4, 1, 0, 3, 0, 0, entry, sn, 56, code, pool},
		words(0), words(pool), []byte("Create\x00\x00"), words(OP_PUSH_CONSTANT, 1, OP_RETURN)[:8], words(OP_RETURN))
	cob, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	insts, err := cob.Disassemble(0)
	if err != nil {
		t.Fatalf("disassemble: %v", err)
	}
	if len(insts) != 2 || insts[0].Opcode != OP_PUSH_CONSTANT || insts[1].Opcode != OP_RETURN {
		t.Errorf("instructions %v", insts)
	}
}

func TestLoadReportsMalformedNames(t *testing.T) {
	// Three scripts: name offset 0 (the header bytes, as the game reads
	// it), past the end of the file, and a name that runs to EOF.
	const code, entry, sn, pool = 44, 48, 60, 72
	data := cobBytes([11]uint32{4, 3, 0, 1, 0, 0, entry, sn, pool, code, pool},
		words(OP_RETURN), words(0, 0, 0), words(0, 5000, pool), []byte("NoTerminator"))
	cob, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cob.ScriptNames[0] != "\x04" {
		t.Errorf("offset 0 name = %q, want the header bytes \"\\x04\"", cob.ScriptNames[0])
	}
	if cob.ScriptNames[1] != "" || cob.ScriptNames[2] != "NoTerminator" {
		t.Errorf("names = %q", cob.ScriptNames)
	}
	var messages []string
	for _, w := range cob.Warnings {
		messages = append(messages, w.String())
	}
	joined := strings.Join(messages, "\n")
	for _, want := range []string{"before the name pool", "past the end of the file", "not NUL-terminated"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q lack %q", joined, want)
		}
	}
}

func TestLoadWarnsOnCodeLengthMismatch(t *testing.T) {
	data := minimalCOB()
	binary.LittleEndian.PutUint32(data[12:], 7)
	cob, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cob.Warnings) != 1 || cob.Warnings[0].Section != "header" {
		t.Errorf("warnings = %v", cob.Warnings)
	}
}

func TestWriteValidatesStructure(t *testing.T) {
	base := func() *COB {
		cob, err := LoadFromReader(bytes.NewReader(minimalCOB()))
		if err != nil {
			t.Fatal(err)
		}
		return cob
	}
	tests := map[string]func(*COB){
		"script count":       func(c *COB) { c.NumScripts = 2 },
		"missing name":       func(c *COB) { c.ScriptNames = nil },
		"piece count":        func(c *COB) { c.NumPieces = 3 },
		"NUL in a name":      func(c *COB) { c.PieceNames[0] = "ba\x00se" },
		"sound names on v4":  func(c *COB) { c.SoundNames = []string{"x"} },
		"entries vs indices": func(c *COB) { c.ScriptCodeIndices = append(c.ScriptCodeIndices, 0) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cob := base()
			mutate(cob)
			if err := cob.WriteToWriter(&bytes.Buffer{}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestWritePadsCodeAndDerivesLength(t *testing.T) {
	cob, err := LoadFromReader(bytes.NewReader(minimalCOB()))
	if err != nil {
		t.Fatal(err)
	}
	cob.Code = append(cob.Code, 0x01, 0x02) // not a whole word
	cob.LengthOfScripts = 99                // stale
	var buf bytes.Buffer
	if err := cob.WriteToWriter(&buf); err != nil {
		t.Fatal(err)
	}
	reread, err := LoadFromReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if reread.LengthOfScripts != 2 || len(reread.Code) != 8 || len(reread.Warnings) != 0 {
		t.Errorf("length %d, code %d bytes, warnings %v", reread.LengthOfScripts, len(reread.Code), reread.Warnings)
	}
	if reread.ScriptNames[0] != "Create" || reread.PieceNames[0] != "base" {
		t.Errorf("names %v %v", reread.ScriptNames, reread.PieceNames)
	}
}

func TestHeaderField5SurvivesRewrite(t *testing.T) {
	data := minimalCOB()
	binary.LittleEndian.PutUint32(data[20:], 0x1234)
	cob, err := LoadFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := cob.WriteToWriter(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("rewrite changed the file")
	}
}
