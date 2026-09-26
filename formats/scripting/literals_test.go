package scripting

import (
	"math"
	"strings"
	"testing"
)

func TestLiteralConversionsMatchRetailCompiler(t *testing.T) {
	// Pairs taken from retail BOS sources and the COBs built from them.
	angles := map[string]int32{"35": 6371, "30": 5461, "105": 19114, "50": 9102, "90": 16384, "-90": -16384, "0": 0}
	for text, want := range angles {
		if got, err := ParseAngleLiteral(text); err != nil || got != want {
			t.Errorf("<%s> = %d, %v; want %d", text, got, err, want)
		}
	}
	linear := map[string]int32{"-2.4": -393216, "2.4": 393216, "500": 81920000, "3.0": 491520, "0.5": 81920, ".5": 81920}
	for text, want := range linear {
		if got, err := ParseLinearLiteral(text); err != nil || got != want {
			t.Errorf("[%s] = %d, %v; want %d", text, got, err, want)
		}
	}
}

func TestLiteralFormattingRoundTrips(t *testing.T) {
	check := func(v int32) {
		a := AngleLiteral(v)
		if got, err := ParseAngleLiteral(strings.Trim(a, "<>")); err != nil || got != v {
			t.Fatalf("angle %d -> %s -> %d, %v", v, a, got, err)
		}
		l := LinearLiteral(v)
		if got, err := ParseLinearLiteral(strings.Trim(l, "[]")); err != nil || got != v {
			t.Fatalf("linear %d -> %s -> %d, %v", v, l, got, err)
		}
	}
	for v := int32(-70000); v <= 70000; v++ {
		check(v)
	}
	for _, v := range []int32{math.MaxInt32, math.MinInt32, 81920000, 1985579, -1368064} {
		check(v)
	}
	if got := AngleLiteral(6371); got != "<35>" {
		t.Errorf("AngleLiteral(6371) = %s, want <35>", got)
	}
	if got := LinearLiteral(393216); got != "[2.4]" {
		t.Errorf("LinearLiteral(393216) = %s, want [2.4]", got)
	}
}

func TestLiteralErrors(t *testing.T) {
	for _, text := range []string{"", "x", "1.2.3", "99999999999"} {
		if _, err := ParseAngleLiteral(text); err == nil {
			t.Errorf("<%s> should not parse", text)
		}
	}
	if _, err := ParseLinearLiteral("20000"); err == nil {
		t.Error("[20000] overflows 32 bits and should not parse")
	}
}
