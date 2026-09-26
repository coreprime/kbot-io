package tdf

import (
	"math"
	"strings"
	"testing"
)

func TestAtolReadsLikeTheGame(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int32
	}{
		{"12abc", 12},
		{" \t-7", -7},
		{"\v\f\r\n 3", 3},
		{"+5", 5},
		{"1.9", 1},
		{"1e3", 1},
		{"0x10", 0},
		{"", 0},
		{"abc", 0},
		{"--1", 0},
		{"- 1", 0},
		{"2147483647", math.MaxInt32},
		{"2147483648", math.MinInt32},
		{"3000000000", -1294967296},
		{"4294967297", 1},
		{"-4294967297", -1},
	} {
		if got := Atol(c.in); got != c.want {
			t.Errorf("Atol(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestAtofReadsLikeTheGame(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
	}{
		{"1.5d2", 150},
		{"1D-1", 0.1},
		{"1E2", 100},
		{".5", 0.5},
		{"-.5", -0.5},
		{"5.", 5},
		{"1.e1", 10},
		{"  -2.25e1x", -22.5},
		{"13O", 13},
		{"1e", 1},
		{"1e+", 1},
		{"1ex", 1},
		{"inf", 0},
		{"nan", 0},
		{"0x10", 0},
		{".", 0},
		{"", 0},
		{"+", 0},
		{strings.Repeat("0", 600) + "5", 5},
		{"0." + strings.Repeat("0", 600) + "5e601", 5},
	} {
		if got := Atof(c.in); got != c.want {
			t.Errorf("Atof(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if got := Atof("1e999"); !math.IsInf(got, 1) {
		t.Errorf("Atof(1e999) = %v, want +Inf", got)
	}
	if got := Atof("-1d999"); !math.IsInf(got, -1) {
		t.Errorf("Atof(-1d999) = %v, want -Inf", got)
	}
	if got := Atof("1e-999"); got != 0 {
		t.Errorf("Atof(1e-999) = %v, want 0", got)
	}
}

func TestFixedScalesAndKeepsTheLowWord(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int32
	}{
		{"1.9", 124518},
		{"-1.9", -124518},
		{"0.15", 9830},
		{"32768", math.MinInt32}, // 2^31 keeps its low word
		{"65536", 0},             // 2^32
		{"1e30", 0},              // out of 64-bit range
		{"1e999", 0},
		{"abc", 0},
	} {
		if got := Fixed(c.in); got != c.want {
			t.Errorf("Fixed(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFlagIsBitZero(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"1", true}, {"3", true}, {"-1", true}, {"1abc", true},
		{"0", false}, {"2", false}, {"true", false}, {"yes", false}, {"on", false}, {"", false},
	} {
		if got := Flag(c.in); got != c.want {
			t.Errorf("Flag(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsNumeral(t *testing.T) {
	for _, s := range []string{"1", "-1.5e3", ".6", "5.", "+2d4", " 7 "} {
		if !isNumeral(s) {
			t.Errorf("isNumeral(%q) = false", s)
		}
	}
	for _, s := range []string{"", ".", "1e", "13O", "1 2", "0x1", "inf", "\v1"} {
		if isNumeral(s) {
			t.Errorf("isNumeral(%q) = true", s)
		}
	}
}
