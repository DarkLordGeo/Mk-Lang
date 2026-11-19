// Why: std.raw's documentation in doc/reference.md must match what raw.mk
// exports and what the checker treats as safe.
// What: TestRawParagraphNamesEveryExport.
// How: reads the reference's std.raw paragraph, raw.mk's exports and
// internal/check's rawSafe table, and compares the three.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The std.raw paragraph names each free function, the
// Atomic type and each of its methods that raw.mk exports, and the names
// it calls safe are exactly the exports internal/check's rawSafe table
// lets a call outside unsafe make (every Atomic method is safe by
// rawUnsafe's owner rule, which the paragraph states as such).
func TestRawParagraphNamesEveryExport(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("raw", "raw.mk"))
	if err != nil {
		t.Fatal(err)
	}
	funcRe := regexp.MustCompile(`(?m)^export ([A-Z]\w*)(?:\[[^\]]*\])?\(`)
	methodRe := regexp.MustCompile(`(?m)^export Atomic\[T\]\.([A-Z]\w*)\(`)
	var funcs, methods []string
	for _, m := range funcRe.FindAllStringSubmatch(string(src), -1) {
		funcs = append(funcs, m[1])
	}
	for _, m := range methodRe.FindAllStringSubmatch(string(src), -1) {
		methods = append(methods, m[1])
	}
	if len(funcs) < 30 || len(methods) < 14 || !strings.Contains(string(src), "export struct Atomic[T: Plain]") {
		t.Fatalf("raw.mk: %d functions, %d Atomic methods; the patterns no longer match it", len(funcs), len(methods))
	}

	ref, err := os.ReadFile(filepath.Join("..", "doc", "reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(ref), "\r\n", "\n")
	start := strings.Index(text, "**`std.raw`**")
	if start < 0 {
		t.Fatal("reference: no **`std.raw`** paragraph")
	}
	end := strings.Index(text[start:], "(E1101).")
	if end < 0 {
		t.Fatal("reference: the std.raw paragraph does not end with its (E1101) sentence")
	}
	para := text[start : start+end]
	tick := regexp.MustCompile("`([A-Z][A-Za-z0-9]*)")
	named := map[string]bool{}
	for _, m := range tick.FindAllStringSubmatch(para, -1) {
		named[m[1]] = true
	}
	for _, n := range append(append([]string{"Atomic"}, funcs...), methods...) {
		if !named[n] {
			t.Errorf("reference's std.raw paragraph does not name %s, which std/raw/raw.mk exports", n)
		}
	}

	i := strings.LastIndex(para, "Of these, ")
	if i < 0 {
		t.Fatal("std.raw paragraph: no \"Of these, ... are safe\" sentence")
	}
	safeText := para[i:]
	if !strings.Contains(safeText, "every method of `Atomic` are safe") {
		t.Errorf("std.raw paragraph's safe sentence does not say every method of Atomic is safe: %q", safeText)
	}
	var docSafe []string
	for _, m := range tick.FindAllStringSubmatch(safeText, -1) {
		if m[1] != "Atomic" {
			docSafe = append(docSafe, m[1])
		}
	}

	chk, err := os.ReadFile(filepath.Join("..", "internal", "check", "concurrency.go"))
	if err != nil {
		t.Fatal(err)
	}
	tbl := regexp.MustCompile(`(?s)var rawSafe = map\[string\]bool\{(.*?)\}`).FindSubmatch(chk)
	if tbl == nil {
		t.Fatal("internal/check/concurrency.go: no rawSafe table")
	}
	var checkSafe []string
	for _, m := range regexp.MustCompile(`"(\w+)": true`).FindAllSubmatch(tbl[1], -1) {
		if slices.Contains(funcs, string(m[1])) {
			checkSafe = append(checkSafe, string(m[1]))
		}
	}
	sort.Strings(docSafe)
	sort.Strings(checkSafe)
	if !slices.Equal(docSafe, checkSafe) {
		t.Errorf("std.raw paragraph calls safe %v; internal/check's rawSafe, over raw.mk's exports, is %v", docSafe, checkSafe)
	}
}
