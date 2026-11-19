// Why: a wrong wc.out that agrees with a wrong wc.mk would pass TestPrograms,
// so the expected output needs a check of its own.
// What: TestWcOutMatchesReference and TestWcReferenceOrder.
// How: counts the words of wc.in in Go (wcReference) and compares the
// result with the committed tests/programs/wc.out.

package corpus

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// wcReference is wc.mk's output for text, computed independently: words are
// the maximal runs of scalars that are not White_Space (str.Words: "Words
// splits on whitespace"; std/core/core_iter.mk skips
// White_Space), counted, sorted by count descending and then by word in byte
// order (wc.mk's ByCount), the first ten printed as "<word> <count>\n".
func wcReference(text string) []byte {
	counts := map[string]int{}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return unicode.Is(unicode.White_Space, r) }) {
		counts[w]++
	}
	words := make([]string, 0, len(counts))
	for w := range counts {
		words = append(words, w)
	}
	slices.SortFunc(words, func(a, b string) int {
		if counts[a] != counts[b] {
			return counts[b] - counts[a]
		}
		return strings.Compare(a, b)
	})
	var b bytes.Buffer
	for i, w := range words {
		if i == 10 {
			break
		}
		fmt.Fprintf(&b, "%s %d\n", w, counts[w])
	}
	return b.Bytes()
}

func TestWcOutMatchesReference(t *testing.T) {
	root := repoRoot(t)
	in, err := readCommitted(filepath.Join(root, ProgramsDir, "wc.in"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := readCommitted(filepath.Join(root, ProgramsDir, "wc.out"))
	if err != nil {
		t.Fatal(err)
	}
	want := wcReference(string(in))
	if bytes.Count(want, []byte("\n")) != 10 {
		t.Fatalf("wc.in has fewer than ten distinct words; the reference printed %q", want)
	}
	if !bytes.Equal(out, want) {
		t.Errorf("%s/wc.out is %q; a Go count of wc.in (top ten, ties by word) gives %q", ProgramsDir, out, want)
	}
}

// The reference itself, on input whose ties, case and White_Space are known.
func TestWcReferenceOrder(t *testing.T) {
	got := string(wcReference("b a\tB a c\n\nb a"))
	if want := "a 3\nb 2\nB 1\nc 1\n"; got != want {
		t.Errorf("wcReference = %q, want %q", got, want)
	}
}
