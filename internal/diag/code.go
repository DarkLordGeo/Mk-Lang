// Why: passes, the corpus and the CLI name errors by code (E0501) and by
// name (exclusive-conflict); both spellings must map to one catalogue row.
//
// What: Code, its String ("E0501"), IsKnown, Name and Entry, ParseCode for
// the reverse, and Catalogue, a copy of every row.
//
// How: a Code is the number itself (E0501 is Code(501)), so String pads to
// four digits and ParseCode reads them back. Lookups go through a map from
// code to row, built once on first use.

package diag

import (
	"strconv"
	"sync"
)

// Code is a catalogue code: E0501 is Code(501), E0000 is Code(0). A Code that
// names no catalogue row is never emitted: New raises an ICE on it. The zero
// Code is E0000 internal-error, a real code, so Code has no "none" value; use
// a separate bool where a code may be absent.
type Code uint16

// String renders "E" plus four zero-padded digits: Code(501) is "E0501".
func (c Code) String() string {
	s := strconv.Itoa(int(c))
	for len(s) < 4 {
		s = "0" + s
	}
	return "E" + s
}

// IsKnown reports whether c is a row of the catalogue.
func (c Code) IsKnown() bool {
	_, ok := catalogueIndex()[c]
	return ok
}

// Name is the catalogue name, "exclusive-conflict" for E0501, or "" for a code
// the catalogue does not hold.
func (c Code) Name() string {
	if i, ok := catalogueIndex()[c]; ok {
		return catalogue[i].Name
	}
	return ""
}

// Entry returns the catalogue row of c and whether c has one.
func (c Code) Entry() (Entry, bool) {
	if i, ok := catalogueIndex()[c]; ok {
		return catalogue[i], true
	}
	return Entry{}, false
}

// ParseCode reads "E0501" back into Code(501). It accepts exactly "E" and four
// digits naming a catalogue row.
func ParseCode(s string) (Code, bool) {
	if len(s) != 5 || s[0] != 'E' {
		return 0, false
	}
	n := 0
	for i := 1; i < 5; i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	c := Code(n)
	if !c.IsKnown() {
		return 0, false
	}
	return c, true
}

// Catalogue returns a copy of every row in catalogue order (E0000, E0001,
// then E0101 upward as doc/errors.md lists them).
func Catalogue() []Entry {
	return append([]Entry(nil), catalogue[:]...)
}

var (
	catalogueOnce sync.Once
	catalogueMap  map[Code]int
)

// catalogueIndex maps each code to its row, built once.
func catalogueIndex() map[Code]int {
	catalogueOnce.Do(func() {
		catalogueMap = make(map[Code]int, len(catalogue))
		for i, e := range catalogue {
			catalogueMap[e.Code] = i
		}
	})
	return catalogueMap
}
