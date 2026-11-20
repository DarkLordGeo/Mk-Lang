// Why: str.ToLower and str.ToUpper's CaseTable is generated from the UCD,
// so a test whose expected values come from the same parse cannot catch a
// wrong field; and the case tests must run where the UCD is absent.
// What: goCase, the simple case mapping from Go's unicode package with the
// pinned Unicode 18.0.0 changes (caseGoChanged) laid over it, and
// TestCaseTableMatchesGo, which checks the committed CaseTable against it.
// How: Go's unicode.ToUpper and ToLower are an oracle independent of the
// UnicodeData.txt parse. The committed rows are read from text_case.mk's
// source and replayed as CaseOf reads them, over every scalar.

package std

import (
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"unicode"
)

// caseGoVersion is the Unicode version of Go's unicode package that
// caseGoChanged is the difference from.
const caseGoVersion = "17.0.0"

// caseGoChanged maps each scalar whose simple case mapping UCD 18.0.0
// changes from Go's unicode 17.0.0 to its 18.0.0 uppercase and lowercase:
// 20 new pairs (closed omega, r with long leg, script r, script r with ring,
// and Latin Extended-G's barred and phonotypic letters).
var caseGoChanged = map[rune][2]rune{
	0x0277: {0xA7DD, 0x0277}, 0x027C: {0xA7E2, 0x027C},
	0xA7DD: {0xA7DD, 0x0277}, 0xA7E2: {0xA7E2, 0x027C},
	0xAB4B: {0xAB6C, 0xAB4B}, 0xAB4C: {0xAB6D, 0xAB4C},
	0xAB6C: {0xAB6C, 0xAB4B}, 0xAB6D: {0xAB6D, 0xAB4C},
	0x1DF40: {0x1DF40, 0x1DF41}, 0x1DF41: {0x1DF40, 0x1DF41},
	0x1DF48: {0x1DF48, 0x1DF49}, 0x1DF49: {0x1DF48, 0x1DF49},
	0x1DF4A: {0x1DF4A, 0x1DF4B}, 0x1DF4B: {0x1DF4A, 0x1DF4B},
	0x1DF4D: {0x1DF4D, 0x1DF4E}, 0x1DF4E: {0x1DF4D, 0x1DF4E},
	0x1DF51: {0x1DF51, 0x1DF52}, 0x1DF52: {0x1DF51, 0x1DF52},
	0x1DF68: {0x1DF68, 0x1DF69}, 0x1DF69: {0x1DF68, 0x1DF69},
	0x1DF6A: {0x1DF6A, 0x1DF6B}, 0x1DF6B: {0x1DF6A, 0x1DF6B},
	0x1DF6C: {0x1DF6C, 0x1DF6D}, 0x1DF6D: {0x1DF6C, 0x1DF6D},
	0x1DF6E: {0x1DF6E, 0x1DF6F}, 0x1DF6F: {0x1DF6E, 0x1DF6F},
	0x1DF72: {0x1DF72, 0x1DF73}, 0x1DF73: {0x1DF72, 0x1DF73},
	0x1DF74: {0x1DF74, 0x1DF75}, 0x1DF75: {0x1DF74, 0x1DF75},
	0x1DF76: {0x1DF76, 0x1DF77}, 0x1DF77: {0x1DF76, 0x1DF77},
	0x1DF78: {0x1DF78, 0x1DF79}, 0x1DF79: {0x1DF78, 0x1DF79},
	0x1DF7A: {0x1DF7A, 0x1DF7B}, 0x1DF7B: {0x1DF7A, 0x1DF7B},
	0x1DF7C: {0x1DF7C, 0x1DF7D}, 0x1DF7D: {0x1DF7C, 0x1DF7D},
	0x1DF7E: {0x1DF7E, 0x1DF7F}, 0x1DF7F: {0x1DF7E, 0x1DF7F},
}

var (
	goCaseOnce sync.Once
	goCaseVal  *simpleCase
)

// goCase is the UCD 18.0.0 simple case mapping as Go's unicode.ToUpper and
// ToLower give it with caseGoChanged laid over, with its CaseTable rows. It
// needs no evidence directory.
func goCase() *simpleCase {
	goCaseOnce.Do(func() {
		sc := &simpleCase{version: "18.0.0", up: map[rune]rune{}, low: map[rune]rune{}}
		for c := rune(0); c <= unicode.MaxRune; c++ {
			up, low := unicode.ToUpper(c), unicode.ToLower(c)
			if m, ok := caseGoChanged[c]; ok {
				up, low = m[0], m[1]
			}
			if up != c {
				sc.up[c] = up
			}
			if low != c {
				sc.low[c] = low
			}
		}
		sc.ranges = sc.buildRanges()
		goCaseVal = sc
	})
	return goCaseVal
}

// caseDiffGo lists, at most 10, the scalars where to differs from goCase.
func caseDiffGo(to func(c rune, upper bool) rune) []string {
	g := goCase()
	var out []string
	for c := rune(0); c <= unicode.MaxRune && len(out) < 10; c++ {
		for _, upper := range []bool{true, false} {
			if got, want := to(c, upper), g.to(c, upper); got != want {
				out = append(out, fmt.Sprintf("U+%04X upper=%v: %04X, Go with the 18.0.0 changes %04X", c, upper, got, want))
			}
		}
	}
	return out
}

var caseRowRE = regexp.MustCompile(`CaseRange\(lo: 0x([0-9A-F]+), hi: 0x([0-9A-F]+), up: (-?\d+), low: (-?\d+)\)`)
var caseLenRE = regexp.MustCompile(`const CaseTable: \[(\d+)\]CaseRange`)

// TestCaseTableMatchesGo reads the committed CaseTable rows of
// core/text_case.mk and requires them, read as CaseOf reads them, to give
// Go's unicode mapping for every scalar but caseGoChanged's, which must
// give their pinned 18.0.0 mapping, uppercase and lowercase both. Each
// pinned entry must also differ from Go's own mapping while Go is at
// caseGoVersion, so the list holds the version gap and nothing else.
func TestCaseTableMatchesGo(t *testing.T) {
	src := readText(t, "core/text_case.mk")
	m := caseLenRE.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("core/text_case.mk declares no CaseTable")
	}
	sc := &simpleCase{}
	for _, r := range caseRowRE.FindAllStringSubmatch(src, -1) {
		lo, _ := strconv.ParseUint(r[1], 16, 32)
		hi, _ := strconv.ParseUint(r[2], 16, 32)
		up, _ := strconv.Atoi(r[3])
		low, _ := strconv.Atoi(r[4])
		row := caseRange{rune(lo), rune(hi), int32(up), int32(low)}
		if n := len(sc.ranges); row.hi < row.lo || n > 0 && row.lo <= sc.ranges[n-1].hi {
			t.Fatalf("CaseTable row %d (%+v) is not ascending and disjoint", n, row)
		}
		sc.ranges = append(sc.ranges, row)
	}
	if strconv.Itoa(len(sc.ranges)) != m[1] {
		t.Fatalf("CaseTable declares %s rows, holds %d", m[1], len(sc.ranges))
	}
	if d := caseDiffGo(sc.caseOf); len(d) > 0 {
		t.Errorf("the committed CaseTable differs from Go's mapping with the 18.0.0 changes:\n%v", d)
	}
	if unicode.Version != caseGoVersion {
		t.Logf("Go's unicode is %s, not %s: the pinned changes are not checked against it", unicode.Version, caseGoVersion)
		return
	}
	for c, want := range caseGoChanged {
		if unicode.ToUpper(c) == want[0] && unicode.ToLower(c) == want[1] {
			t.Errorf("U+%04X is listed as changed in 18.0.0, but Go's %s maps it the same", c, caseGoVersion)
		}
	}
	t.Logf("%d rows; equal to Go's unicode %s but for the %d pinned 18.0.0 changes", len(sc.ranges), unicode.Version, len(caseGoChanged))
}
