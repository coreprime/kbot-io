package tdf

import (
	"reflect"
	"strings"
	"testing"
)

func TestElementNames(t *testing.T) {
	cases := []struct {
		stem  string
		names []string
		want  []string
	}{
		{"special", []string{"", "", ""}, []string{"special0", "special1", "special2"}},
		{"special", []string{"special1", ""}, []string{"special1", "special0"}},
		{"Schema ", []string{"Schema 0", "", "SCHEMA 2", ""}, []string{"Schema 0", "Schema 1", "SCHEMA 2", "Schema 3"}},
		{"unit", []string{"startpos1", "", "unit0"}, []string{"startpos1", "unit1", "unit0"}},
		{"", []string{"a", ""}, []string{"a", ""}},
		{"x", nil, nil},
	}
	for _, c := range cases {
		got := ElementNames(c.stem, c.names)
		if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("ElementNames(%q, %q) = %q, want %q", c.stem, c.names, got, c.want)
		}
	}
}

type namedItem struct {
	Key string `tdf:",name"`
	V   int    `tdf:"v,omitempty"`
}

func TestSectionsCatchAllNamesUnnamedElements(t *testing.T) {
	type doc struct {
		Items []namedItem `tdf:"item,sections"`
	}
	out, err := Marshal(doc{Items: []namedItem{{Key: "item1", V: 1}, {V: 2}, {V: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "[]") || !strings.Contains(s, "[item0]") || !strings.Contains(s, "[item2]") {
		t.Fatalf("unnamed elements:\n%s", s)
	}
	var back doc
	if err := Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	want := []namedItem{{Key: "item1", V: 1}, {Key: "item0", V: 2}, {Key: "item2", V: 3}}
	if !reflect.DeepEqual(back.Items, want) {
		t.Errorf("read back %+v, want %+v", back.Items, want)
	}

	// The tag key does not filter: a child of any name is still caught.
	var odd doc
	if err := Unmarshal([]byte("[startpos1]{v=4;}[item7]{v=5;}"), &odd); err != nil {
		t.Fatal(err)
	}
	if len(odd.Items) != 2 || odd.Items[0].Key != "startpos1" || odd.Items[1].Key != "item7" {
		t.Errorf("caught %+v", odd.Items)
	}
}

func TestRepeatedFieldNamesDoNotCollide(t *testing.T) {
	type doc struct {
		Schemas []namedItem `tdf:"Schema "`
	}
	out, err := Marshal(doc{Schemas: []namedItem{{Key: "Schema 1", V: 1}, {V: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Count(s, "[Schema 1]") != 1 || !strings.Contains(s, "[Schema 0]") {
		t.Errorf("names:\n%s", s)
	}
}

func TestRepeatsCountKey(t *testing.T) {
	type withMeta struct {
		Items []namedItem       `tdf:"Item,repeats=ITEMCOUNT"`
		Extra map[string]string `tdf:",remaining"`
		Meta  Meta              `tdf:",meta"`
	}
	type metaOnly struct {
		Items []namedItem `tdf:"Item,repeats=ITEMCOUNT"`
		Meta  Meta        `tdf:",meta"`
	}

	// Decoded with a Meta: the count is kept as written, even when it is
	// not the number of sections, and a source without one stays without.
	var d withMeta
	if err := Unmarshal([]byte("itemcount=5;[Item 0]{v=1;}"), &d); err != nil {
		t.Fatal(err)
	}
	if d.Extra["itemcount"] != "5" {
		t.Errorf("catch-all: %v", d.Extra)
	}
	if out := string(mustMarshal(t, d)); strings.Count(out, "itemcount=5;") != 1 || strings.Contains(out, "ITEMCOUNT") {
		t.Errorf("kept count:\n%s", out)
	}
	var none withMeta
	if err := Unmarshal([]byte("[Item 0]{v=1;}"), &none); err != nil {
		t.Fatal(err)
	}
	if !none.Meta.Decoded() {
		t.Error("Decoded is false after Unmarshal")
	}
	if out := string(mustMarshal(t, none)); strings.Contains(out, "ITEMCOUNT") {
		t.Errorf("count added to a source without one:\n%s", out)
	}

	// Without a catch-all, the Meta's text is written.
	var m metaOnly
	if err := Unmarshal([]byte("ITEMCOUNT=7;[Item 0]{}"), &m); err != nil {
		t.Fatal(err)
	}
	if out := string(mustMarshal(t, m)); !strings.Contains(out, "ITEMCOUNT=7;") {
		t.Errorf("meta-only count:\n%s", out)
	}

	// Built in code: the number of elements.
	var fresh withMeta
	if fresh.Meta.Decoded() {
		t.Error("Decoded is true for the zero Meta")
	}
	fresh.Items = []namedItem{{}, {}}
	if out := string(mustMarshal(t, fresh)); !strings.Contains(out, "ITEMCOUNT=2;") {
		t.Errorf("fresh count:\n%s", out)
	}
}

func TestMapSectionKeepsNestedSections(t *testing.T) {
	type weapon struct {
		Key    string         `tdf:",name"`
		ID     int            `tdf:"id"`
		Damage map[string]int `tdf:"DAMAGE,omitempty"`
		Meta   Meta           `tdf:",meta"`
	}
	src := "[W]{ID=1;[DAMAGE]{default=1;[X]{a=1;}armcom=5;}}"
	var ws []weapon
	if err := Unmarshal([]byte(src), &ws); err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Damage["armcom"] != 5 || len(ws[0].Damage) != 2 {
		t.Fatalf("decoded %+v", ws)
	}
	out := string(mustMarshal(t, ws))
	if ok, msg := SemanticEqual([]byte(src), []byte(out)); !ok {
		t.Errorf("nested section lost: %s\n%s", msg, out)
	}
	if i, j, k := strings.Index(out, "default"), strings.Index(out, "[X]"), strings.Index(out, "armcom"); i >= j || j >= k {
		t.Errorf("nested section moved:\n%s", out)
	}

	// An edit to the map keeps the nested section.
	ws[0].Damage["armcom"] = 9
	out = string(mustMarshal(t, ws))
	if !strings.Contains(out, "[X]") || !strings.Contains(out, "armcom=9;") {
		t.Errorf("after edit:\n%s", out)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	out, err := Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return out
}
