package common

import "strings"

// Warning describes game data that the game reads differently from how it
// looks, or never reads at all: a value too long for the game's buffer, a
// number the game narrows, a section it cannot find. The data still loads;
// warnings point at what an author probably did not intend.
type Warning struct {
	// Section is the path of the section concerned, outermost first and
	// joined with '/' (for example "GlobalHeader/Schema 2/specials"); empty
	// for the file as a whole.
	Section string
	// Key is the key concerned, or empty when the warning is about a section.
	Key string
	// Message says what the game does.
	Message string
}

// String formats the warning as "[section] key: message".
func (w Warning) String() string {
	var b strings.Builder
	if w.Section != "" {
		b.WriteString("[" + w.Section + "] ")
	}
	if w.Key != "" {
		b.WriteString(w.Key + ": ")
	}
	b.WriteString(w.Message)
	return b.String()
}
