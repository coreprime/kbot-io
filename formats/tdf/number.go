package tdf

import (
	"math"
	"strconv"
)

// This file holds the number readers the game applies to TDF values. The game
// never rejects a value for its syntax: it reads the longest numeric prefix
// and ignores the rest, so "13O" is 13, "canmove=true" is 0 and a value with
// no digits at all is 0. Use these readers wherever a value must mean what it
// means to the game rather than what Go's strict strconv parsers accept.

// isCSpace reports whether c is skipped before a number, as the C library's
// isspace does in the "C" locale: space, tab, newline, vertical tab, form feed
// and carriage return.
func isCSpace(c byte) bool {
	return c == ' ' || (c >= '\t' && c <= '\r')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Atol reads an integer value the way the game does (C atol with a 32-bit
// long). Leading C whitespace and one sign are skipped, then decimal digits are
// read up to the first other character; overflow wraps modulo 2^32 and no
// digits read as 0. So "12abc" is 12, "1.9" is 1, "1e3" is 1, "0x10" is 0 and
// "4294967297" is 1.
func Atol(s string) int32 {
	i := 0
	for i < len(s) && isCSpace(s[i]) {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
	}
	var total uint32
	for ; i < len(s) && isDigit(s[i]); i++ {
		total = total*10 + uint32(s[i]-'0')
	}
	if neg {
		total = -total
	}
	return int32(total)
}

// Atof reads a floating-point value the way the game does (C atof). Leading C
// whitespace is skipped, then only [sign] digits [. digits] [(e|E|d|D) [sign]
// digits] is read and the rest ignored. There is no hexadecimal, "inf" or
// "nan" form, and text with no digits in its mantissa reads as 0. So "1.5d2" is
// 150, ".5" is 0.5, "5." is 5, "13O" is 13, "1e" is 1 and "inf" is 0. A value
// too large for a float64 reads as ±Inf. The result is correctly rounded
// whatever the numeral's length.
func Atof(s string) float64 {
	num, _ := numeralPrefix(s)
	if num == "" {
		return 0
	}
	// Only a range error is possible here, and ParseFloat still returns the
	// saturated value (±Inf, or 0 on underflow), which is what C gives.
	f, _ := strconv.ParseFloat(num, 64)
	return f
}

// numeralPrefix extracts the numeric prefix Atof reads from s and rewrites it
// in a form strconv.ParseFloat accepts: a 'd' or 'D' exponent becomes 'e'. It
// returns "" when s has no digits in its mantissa, and end, the index in s
// just past the prefix (0 when there is none).
func numeralPrefix(s string) (num string, end int) {
	i := 0
	for i < len(s) && isCSpace(s[i]) {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	digits := false
	for i < len(s) && isDigit(s[i]) {
		i++
		digits = true
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
			digits = true
		}
	}
	if !digits {
		return "", 0
	}
	mant := s[start:i]
	// A lone '.' after the digits ("5.") is legal C but not Go syntax.
	if mant[len(mant)-1] == '.' {
		mant = mant[:len(mant)-1]
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E' || s[i] == 'd' || s[i] == 'D') {
		j := i + 1
		if j < len(s) && (s[j] == '-' || s[j] == '+') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			return mant + "e" + s[i+1:j], j
		}
	}
	return mant, i
}

// Fixed reads a value as 16.16 fixed point the way the game does for fields it
// stores in that form: Atof(s) times 65536, truncated toward zero to 64 bits,
// keeping the low 32 bits. A product outside the 64-bit range (or ±Inf) gives
// 0, the low word of the C conversion's out-of-range result.
func Fixed(s string) int32 {
	v := Atof(s) * 65536
	const limit = 9223372036854775808.0 // 2^63
	if !(v > -limit && v < limit) {
		return 0 // INT64_MIN has a zero low word
	}
	return int32(uint32(uint64(int64(math.Trunc(v)))))
}

// Flag reads a boolean value the way the game does: bit 0 of Atol(s). So "1"
// and "3" are true while "0", "2", "true" and "yes" are false.
func Flag(s string) bool {
	return Atol(s)&1 != 0
}

// isNumeral reports whether all of s (after trimming TDF separators) is a
// number in the grammar Atof reads, with nothing left over.
func isNumeral(s string) bool {
	s = trimSeparators(s)
	if s == "" || isCSpace(s[0]) {
		return false
	}
	num, end := numeralPrefix(s)
	return num != "" && end == len(s)
}
