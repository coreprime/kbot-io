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
