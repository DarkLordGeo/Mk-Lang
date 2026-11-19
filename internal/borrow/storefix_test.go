// Why: a `*x into p` fix must insert its clause before any cpu or section
// clause, which come last in a signature.
// What: TestClauseBeforeCpuSection, for storefix.go's trailingClause.
// How: a table of signature texts and the offset of the trailing clause.

package borrow

import "testing"

// TestClauseBeforeCpuSection: trailingClause finds a Cpu or Section clause
// that ends a signature's text, so clauseSlot backs over both (section after
// cpu), and nothing else: a word that only ends in cpu, a string with an
// escape, a result type.
func TestClauseBeforeCpuSection(t *testing.T) {
	for _, tc := range []struct {
		text, word string
		at         int
		ok         bool
	}{
		{`F(x: &i32) cpu "avx2"`, "cpu", 11, true},
		{`F(x: &i32) section ".text.x"`, "section", 11, true},
		{`F(x: &i32) cpu "avx2" section ".text.x"`, "section", 22, true},
		{`F(x: &i32) cpu "avx2"`, "section", 0, false},
		{`F(x: &i32) mycpu "avx2"`, "cpu", 0, false},
		{`F(x: &i32) cpu "a\"b"`, "cpu", 0, false},
		{`F(x: &i32)cpu"avx2"`, "cpu", 10, true},
		{`F(x: &i32): str`, "cpu", 0, false},
	} {
		at, ok := trailingClause(tc.text, tc.word)
		if ok != tc.ok || ok && at != tc.at {
			t.Errorf("%q %s: got %d %v, want %d %v", tc.text, tc.word, at, ok, tc.at, tc.ok)
		}
	}
}
