package tdf

// isSeparator reports whether c separates tokens: space, tab, CR or LF.
func isSeparator(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// trimSeparators trims spaces, tabs, CRs and LFs (and nothing else) from both
// ends of s, as the game trims keys, values and section names.
func trimSeparators(s string) string {
	i, j := 0, len(s)
	for i < j && isSeparator(s[i]) {
		i++
	}
	for j > i && isSeparator(s[j-1]) {
		j--
	}
	return s[i:j]
}

// trimSeparatorBytes is trimSeparators for a byte slice.
func trimSeparatorBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && isSeparator(b[i]) {
		i++
	}
	for j > i && isSeparator(b[j-1]) {
		j--
	}
	return b[i:j]
}

// foldKey upper-cases the ASCII letters of s and leaves every other byte as
// is. TDF keys and section names match ignoring ASCII case only; bytes above
// 0x7F (CP1252 letters in game data) are compared exactly.
func foldKey(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'a' && c <= 'z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'a' && b[j] <= 'z' {
					b[j] -= 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// equalFold reports whether a and b are equal ignoring ASCII case only.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
