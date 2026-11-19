// Why: a const whose evaluated value points at interior-mutable data or a raw
// pointer would let a shared constant change; E1002 refuses it.
// What: tests of E1002 on a const's evaluated value.
// How: checks the programs in testdata/e1002 and compares the diagnostic
// positions that check's EvalConst and constPointee report.

package driver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestE1002_ConstPointeeIsReadOnly: o1 (`const GC: &Cell[int]` made in
// EvalArena) and o2 (a `?&Holder` whose Holder holds a Cell inline) are
// E1002 at the const's name, since a store through them would fault on the
// read-only data; ok.mk (a const of DefaultCommand's shape whose `?&` field
// and `&fs.File` variant are not taken, and a `&int` pointee) checks clean.
func TestE1002_ConstPointeeIsReadOnly(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ file, want string }{
		{"o1.mk", "main.mk:8:7:E1002"},
		{"o2.mk", "main.mk:12:7:E1002"},
		{"ok.mk", ""},
	} {
		t.Run(c.file, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(filepath.Join("testdata", "e1002", c.file))
			if err != nil {
				t.Fatal(err)
			}
			r := checkText(t, string(src))
			got := ""
			for i, l := range baseLines(r) {
				if i > 0 {
					got += " "
				}
				got += l
			}
			if got != c.want {
				t.Errorf("got %q, want %q\n%s", got, c.want, r.Report.Text(r.Sources))
			}
		})
	}
}
