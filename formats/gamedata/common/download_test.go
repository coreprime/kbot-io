package common

import (
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/tdf"
)

// The game reads the first five sections of a download file whatever their
// names; all of them are kept, in order, so a round trip changes nothing.
func TestDownloadFileKeepsEverySection(t *testing.T) {
	src := `[ENTRY0]{UNITMENU=ARMCOM;MENU=2;BUTTON=300;UNITNAME=ARMFARK;}
[MENUENTRY1]{UNITMENU=ARMCK;MENU=0;BUTTON=0;UNITNAME=ARMFARK;extra=1;}
[MENUENTRY2]{UNITNAME=ORPHAN;}
[MENUENTRY3]{UNITMENU=A;}
[MENUENTRY4]{UNITMENU=B;}
[MENUENTRY5]{UNITMENU=C;}`
	var d DownloadFile
	if err := tdf.Unmarshal([]byte(src), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Entries) != 6 || d.Entries[0].Key != "ENTRY0" {
		t.Fatalf("entries: %+v", d.Entries)
	}
	if got := d.Menus(); len(got) != DownloadMenuLimit || got[4].UnitMenu != "B" {
		t.Errorf("menus: %+v", got)
	}
	out, err := tdf.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	if ok, msg := tdf.SemanticEqual([]byte(src), out); !ok {
		t.Errorf("round trip: %s\n%s", msg, out)
	}
	if !strings.Contains(string(out), "MENU=0;") {
		t.Errorf("explicit MENU=0 dropped:\n%s", out)
	}
	var msgs []string
	for _, w := range d.Check() {
		msgs = append(msgs, w.String())
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"BUTTON: 300", "[MENUENTRY2] UNITMENU", "[MENUENTRY5]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Check misses %q:\n%s", want, joined)
		}
	}
	e := d.AddEntry(MenuEntry{UnitMenu: "X"})
	if e.Key != "MENUENTRY7" {
		t.Errorf("AddEntry named %q", e.Key)
	}
}

// Entries built in code without a name are written as [MENUENTRY<n>], never
// as an unnamed [] section, and read back unchanged.
func TestUnnamedDownloadEntriesAreNamed(t *testing.T) {
	d := DownloadFile{Entries: []MenuEntry{
		{UnitMenu: "ARMCOM", UnitName: "ARMFARK"},
		{Key: "MENUENTRY0", UnitMenu: "ARMCK", UnitName: "ARMFARK"},
	}}
	out, err := tdf.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "[]") || !strings.Contains(s, "[MENUENTRY1]") || strings.Count(s, "[MENUENTRY0]") != 1 {
		t.Fatalf("names:\n%s", s)
	}
	var back DownloadFile
	if err := tdf.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 2 || back.Entries[0].Key != "MENUENTRY1" || back.Entries[0].UnitMenu != "ARMCOM" {
		t.Fatalf("read back: %+v", back.Entries)
	}
	again, err := tdf.Marshal(&back)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != s {
		t.Errorf("second write differs:\n%s\n---\n%s", s, again)
	}
}

func TestWarningString(t *testing.T) {
	w := Warning{Section: "GlobalHeader/Schema 1", Key: "type", Message: "m"}
	if w.String() != "[GlobalHeader/Schema 1] type: m" {
		t.Errorf("%q", w.String())
	}
	if (Warning{Message: "m"}).String() != "m" {
		t.Error("bare message")
	}
}
