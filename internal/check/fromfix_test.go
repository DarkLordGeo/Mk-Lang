// Why: E0601's fix, the minimal `from` clause, is computed by check from
// the body, and mk fix applies it.
// What: TestE0601_FromFix.
// How: checks functions without a clause and reads the fix: its text, its
// place after the result type, and whether it is proven or only compiles.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// TestE0601_FromFix: E0601's fix is the minimal `from` clause read from the
// body, inserted right after the result type: proven for a private function,
// compiles for an exported one; every candidate when a returned value is not
// a path from a parameter; none when the signature already writes another
// clause. mk fix applies it to internal/driver/testdata/fix/from.mk.
func TestE0601_FromFix(t *testing.T) {
	cases := []struct {
		name, src string
		class     diag.FixClass // 0: no fix
		clause    string
		line, col int // where the clause is inserted (1-based)
	}{
		{"both returned (from.mk)", "Longer(a: str, b: str): str {\n\tif a.Len() > b.Len() {\n\t\treturn a\n\t}\n\treturn b\n}\n", diag.Proven, " from a, b", 1, 28},
		{"one returned", "Longer(a: &str, b: &str): &str {\n\treturn a\n}\n", diag.Proven, " from a", 1, 31},
		{"paths", "Pick(a: []u8, b: []u8, i: int): []u8 {\n\tif i > 0 {\n\t\treturn (b[1..])\n\t}\n\treturn b[0..i]\n}\n", diag.Proven, " from b", 1, 37},
		{"call result", "Id(s: str): str from s {\n\treturn s\n}\n\nLonger(a: str, b: str): str {\n\treturn Id(a)\n}\n", diag.Proven, " from a, b", 5, 28},
		{"literal's own return", "Longer(a: str, b: str): str {\n\tf := fn(x: int): int { return x }\n\t_ = f\n\treturn a\n}\n", diag.Proven, " from a", 1, 28},
		{"exported", "export Longer(a: str, b: str): str {\n\treturn b\n}\n", diag.Compiles, " from b", 1, 35},
		{"self", "struct T {\n\ts: str\n}\n\nT.Pick(&self, b: &str): &str {\n\treturn &self.s\n}\n", diag.Proven, " from self", 5, 29},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, ds := capDiags(t, tc.src)
			var d *diag.Diagnostic
			for _, x := range ds {
				if x.Code == diag.E0601 {
					if d != nil {
						t.Fatalf("two E0601\n%s", dump(w, ds))
					}
					d = x
				} else {
					t.Fatalf("unexpected %s\n%s", x.Code, dump(w, ds))
				}
			}
			if d == nil {
				t.Fatalf("no E0601\n%s", dump(w, ds))
			}
			if len(d.Fixes) != 1 {
				t.Fatalf("fixes %d, want 1\n%s", len(d.Fixes), dump(w, ds))
			}
			f := d.Fixes[0]
			if f.Class != tc.class || len(f.Edits) != 1 || f.Edits[0].Text != tc.clause {
				t.Fatalf("fix %v %+v, want %v %q", f.Class, f.Edits, tc.class, tc.clause)
			}
			e := f.Edits[0].Span
			if e.Start != e.End {
				t.Fatalf("edit %v is not an insertion", e)
			}
			// The insertion point is the end of the result type: the
			// line and column of the source as written.
			pre := tc.src[:e.Start]
			line := strings.Count(pre, "\n") + 1
			col := int(e.Start) - strings.LastIndex(pre, "\n")
			if line != tc.line || col != tc.col {
				t.Fatalf("insertion at %d:%d, want %d:%d (%q|%q)", line, col, tc.line, tc.col, pre[strings.LastIndex(pre, "\n")+1:], tc.src[e.Start:])
			}
			// Applying the fix removes the E0601 and adds nothing.
			fixed := tc.src[:e.Start] + tc.clause + tc.src[e.Start:]
			w2, ds2 := capDiags(t, fixed)
			if len(ds2) != 0 {
				t.Fatalf("after the fix:\n%s\n%s", fixed, dump(w2, ds2))
			}
		})
	}
	// A fn type has no body and no clause place the fix could write:
	// alternatives only.
	w, ds := capDiags(t, "G(f: fn(a: &str, b: &str): &str) {\n}\n")
	for _, x := range ds {
		if x.Code == diag.E0601 && len(x.Fixes) != 0 {
			t.Fatalf("fix offered for a fn type\n%s", dump(w, ds))
		}
	}
}
