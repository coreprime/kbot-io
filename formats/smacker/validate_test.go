package smacker_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/coreprime/kbot-io/formats/smacker"
)

// TestVersion checks the version accessor and that SMK4 still opens.
func TestVersion(t *testing.T) {
	for _, c := range []struct {
		sig  string
		want int
	}{{"SMK2", 2}, {"SMK4", 4}} {
		s := defaultSpec()
		s.sig = c.sig
		r := s.open(t)
		if got := r.Version(); got != c.want {
			t.Errorf("%s: Version = %d, want %d", c.sig, got, c.want)
		}
		if got := r.Header().Version(); got != c.want {
			t.Errorf("%s: Header.Version = %d, want %d", c.sig, got, c.want)
		}
	}
	s := defaultSpec()
	s.sig = "SMK4"
	if info := s.open(t).Info(); !strings.Contains(info, "SMK4 (TA plays SMK2 only)") {
		t.Errorf("Info() does not flag SMK4:\n%s", info)
	}
}

// TestValidateAcceptsWellFormed checks that a well-formed SMK2 passes, with
// or without trailing bytes after the payloads.
func TestValidateAcceptsWellFormed(t *testing.T) {
	s := defaultSpec()
	if err := s.open(t).Validate(smacker.DefaultLimits()); err != nil {
		t.Errorf("Validate: %v", err)
	}
	s.extra = 3
	if err := s.open(t).Validate(smacker.DefaultLimits()); err != nil {
		t.Errorf("Validate with trailing bytes: %v", err)
	}
}

// TestValidateReportsProblems checks each structural check in turn.
func TestValidateReportsProblems(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*smkSpec)
		limits func(*smacker.Limits)
		want   error
		text   string
	}{
		{name: "SMK4", mutate: func(s *smkSpec) { s.sig = "SMK4" }, want: smacker.ErrUnsupportedVersion, text: "SMK4"},
		{name: "zero width", mutate: func(s *smkSpec) { s.width = 0 }, want: smacker.ErrOutOfBounds, text: "0x32"},
		{name: "zero height", mutate: func(s *smkSpec) { s.height = 0 }, want: smacker.ErrOutOfBounds, text: "64x0"},
		{name: "wide", mutate: func(s *smkSpec) { s.width = 4097 }, want: smacker.ErrOutOfBounds, text: "width 4097"},
		{name: "tall", mutate: func(s *smkSpec) { s.height = 5000 }, want: smacker.ErrOutOfBounds, text: "height 5000"},
		{name: "no frames", mutate: func(s *smkSpec) {
			s.frames = 0
			s.frameSizes = nil
			s.frameTypes = nil
		}, want: smacker.ErrOutOfBounds, text: "frame count is 0"},
		{name: "too many frames", mutate: func(s *smkSpec) {}, limits: func(l *smacker.Limits) { l.MaxFrames = 2 },
			want: smacker.ErrOutOfBounds, text: "frame count 3 exceeds 2"},
		{name: "large trees", mutate: func(s *smkSpec) {}, limits: func(l *smacker.Limits) { l.MaxTreeBytes = 4 },
			want: smacker.ErrOutOfBounds, text: "Huffman trees are 7 bytes"},
		{name: "large frame", mutate: func(s *smkSpec) {}, limits: func(l *smacker.Limits) { l.MaxFrameBytes = 10 },
			want: smacker.ErrOutOfBounds, text: "2 frame payloads exceed 10 bytes (first: entry 0, 12 bytes)"},
		{name: "large file", mutate: func(s *smkSpec) {}, limits: func(l *smacker.Limits) { l.MaxFileBytes = 100 },
			want: smacker.ErrOutOfBounds, text: "limit 100"},
		{name: "payloads past the end", mutate: func(s *smkSpec) { s.noPayload = true },
			want: smacker.ErrTruncated, text: "frame payloads end at byte"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := defaultSpec()
			c.mutate(&s)
			limits := smacker.DefaultLimits()
			if c.limits != nil {
				c.limits(&limits)
			}
			err := s.open(t).Validate(limits)
			if !errors.Is(err, c.want) {
				t.Fatalf("Validate = %v, want %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.text) {
				t.Errorf("Validate = %q, want it to mention %q", err, c.text)
			}
		})
	}
}

// TestValidateZeroLimits checks that zero limits disable the bound checks
// while the structural ones still apply, and that several problems are all
// reported.
func TestValidateZeroLimits(t *testing.T) {
	s := defaultSpec()
	s.width = 1 << 20
	if err := s.open(t).Validate(smacker.Limits{}); err != nil {
		t.Errorf("Validate with zero limits: %v", err)
	}

	s = defaultSpec()
	s.sig = "SMK4"
	s.noPayload = true
	data := s.bytes()
	r, err := smacker.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	err = r.Validate(smacker.Limits{})
	if !errors.Is(err, smacker.ErrUnsupportedVersion) || !errors.Is(err, smacker.ErrTruncated) {
		t.Errorf("Validate = %v, want both ErrUnsupportedVersion and ErrTruncated", err)
	}
}
