package common

import "fmt"

// ValidTrailer reports whether b is a copyright trailer TA 3.1c accepts:
// exactly TrailerSize bytes reading "Copyright ____ Cavedog Entertainment",
// where the four year characters may be anything.
func ValidTrailer(b []byte) bool {
	if len(b) != TrailerSize {
		return false
	}
	for i := 0; i < TrailerSize; i++ {
		if i >= TrailerYearOffset && i < TrailerYearOffset+TrailerYearSize {
			continue
		}
		if b[i] != DefaultTrailer[i] {
			return false
		}
	}
	return true
}

// Trailer builds the copyright trailer for the given four-digit year, for
// example Trailer(1998) returns "Copyright 1998 Cavedog Entertainment".
func Trailer(year int) ([]byte, error) {
	if year < 0 || year > 9999 {
		return nil, fmt.Errorf("trailer year %d does not fit in four digits", year)
	}
	b := []byte(DefaultTrailer)
	copy(b[TrailerYearOffset:], fmt.Sprintf("%04d", year))
	return b, nil
}
