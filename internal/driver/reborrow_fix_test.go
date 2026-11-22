// Why: a `mut T` where a `&T` is expected has no conversion, and E0301 must
// name the rewrite, the shared reborrow `&*p`; mk fix may write it only where
// no loan of the statement can conflict with the new shared one.
// What: TestReborrowProvenInReturn, TestReborrowCompilesElsewhere,
// TestReborrowConflictsNotApplied and TestReborrowNoFix, with applyFix.
// How: each program is checked; a proven fix is run through mk fix to a
// clean package, and a compiles fix is applied by hand and rechecked, or,
// where it meets another loan, shown to be left alone by mk fix.

package driver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mk/internal/diag"
)

const reborrowNode = "struct Node {\n    v: int\n}\n\nRead(n: &Node): int {\n    return n.v\n}\n\n"

// reborrowDiag checks src and returns its one E0301 for a `mut T` met by a
// `&T`, which has one try line naming a `&` rewrite. Another E0301 may
// stand beside it: a record literal holding the field mismatches as well.
func reborrowDiag(t *testing.T, src string) *diag.Diagnostic {
	t.Helper()
	r := checkText(t, src)
	var d *diag.Diagnostic
	for _, e := range r.Report.Diags {
		if e.Diag.Code != diag.E0301 {
			t.Fatalf("want E0301 only:\n%s", diagLines(r))
		}
		if strings.Contains(e.Diag.Message, "found `mut ") {
			if d != nil {
				t.Fatalf("want one E0301 for the reference:\n%s", diagLines(r))
			}
			d = e.Diag
		}
	}
	if d == nil || len(d.Alternatives) != 1 || !strings.Contains(d.Alternatives[0], "&") {
		t.Fatalf("want an E0301 with one try line naming `&`:\n%s", diagLines(r))
	}
	return d
}

// applyFix writes the edits of f into src, which is the one checked file.
func applyFix(src string, f diag.Fix) string {
	es := append([]diag.Edit(nil), f.Edits...)
	sort.Slice(es, func(i, j int) bool { return es[i].Span.Start > es[j].Span.Start })
	for _, e := range es {
		src = src[:int(e.Span.Start)] + e.Text + src[int(e.Span.End):]
	}
	return src
}

// TestReborrowProvenInReturn: the whole operand of a `return`, read through
// a parameter or a `try` call result in a body with no `defer`, gets the
// proven fix, which mk fix writes to a clean package.
func TestReborrowProvenInReturn(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, src, want string }{
		{"parameter", reborrowNode + "Get(n: mut Node): &Node from n {\n    return n\n}\n", "    return &*n\n"},
		{"try", reborrowNode + "Make(a: &Arena): OutOfMemory!&Node from a {\n    return try a.Put(Node(v: 1))\n}\n", "    return &*try a.Put(Node(v: 1))\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := checkText(t, c.src)
			var d *diag.Diagnostic
			for _, e := range r.Report.Diags {
				if e.Diag.Code == diag.E0301 {
					d = e.Diag
				}
			}
			if d == nil || len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Proven {
				t.Fatalf("want an E0301 with one proven fix:\n%s", diagLines(r))
			}
			if out := fixToClean(t, c.src); !strings.Contains(out, c.want) {
				t.Errorf("fixed text, want %q:\n%s", c.want, out)
			}
		})
	}
}

// TestReborrowCompilesElsewhere: where the fix is not proven it is compiles,
// and applied it checks: a returned local (which may hold a loan of another
// local), an argument, a declared local, a struct literal field, a record
// shorthand field (written `n: &*n`), a `try` result, an `else`
// (parenthesised) and a loan `mut t`.
func TestReborrowCompilesElsewhere(t *testing.T) {
	t.Parallel()
	holder := "struct Holder {\n    n: &Node\n}\n\n"
	cases := []struct{ name, src, want, alt string }{
		{"returnlocal", "struct Node {\n    next: ?&Node\n    v: int\n}\n\nPush(a: &Arena, head: ?&Node into a, v: int): OutOfMemory!&Node from a, head {\n    p := try a.Put(Node(next: head, v: v))\n    return p\n}\n", "    return &*p\n", "`&*p`"},
		{"argument", reborrowNode + "Bump(n: mut Node): int {\n    return Read(n)\n}\n", "Read(&*n)", "`&*n`"},
		{"declared", reborrowNode + "Bump(n: mut Node): int {\n    q: &Node = n\n    return q.v\n}\n", "q: &Node = &*n", "`&*n`"},
		{"field", reborrowNode + holder + "Bump(n: mut Node): int {\n    h := Holder(n: n)\n    return h.n.v\n}\n", "Holder(n: &*n)", "`&*n`"},
		{"shorthand", reborrowNode + "Take(r: (n: &Node, k: int)): int {\n    return r.k + r.n.v\n}\n\nPass(n: mut Node): int {\n    k := 1\n    return Take((n, k))\n}\n", "Take((n: &*n, k))", "`n: &*n`"},
		{"try", reborrowNode + "Make(a: &Arena): OutOfMemory!int {\n    return Read(try a.Put(Node(v: 1)))\n}\n", "Read(&*try a.Put(Node(v: 1)))", "before it"},
		{"else", reborrowNode + "Pick(o: ?mut Node): int {\n    q: &Node = o else {\n        return 0\n    }\n    return q.v\n}\n", "q: &Node = &*(o else {\n        return 0\n    })", "`&*(o else { .. })`"},
		{"loan", reborrowNode + "Use(): int {\n    var t := Node(v: 1)\n    return Read(mut t)\n}\n", "Read(&t)", "`&t`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			d := reborrowDiag(t, c.src)
			if len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Compiles {
				t.Fatalf("want one compiles fix, got %+v", d.Fixes)
			}
			if !strings.Contains(d.Alternatives[0], c.alt) {
				t.Errorf("try line %q does not name %s", d.Alternatives[0], c.alt)
			}
			out := applyFix(c.src, d.Fixes[0])
			if !strings.Contains(out, c.want) {
				t.Fatalf("applied text, want %q:\n%s", c.want, out)
			}
			if r := checkText(t, out); len(r.Report.Diags) != 0 {
				t.Errorf("the applied fix does not check:\n%s\n%s", out, diagLines(r))
			}
		})
	}
}

// TestReborrowConflictsNotApplied: where the new shared loan can meet
// another loan or outlive what it points at, the fix is not proven and mk
// fix leaves the program as it was: `Both(p, p)`, a reborrow held across a
// write through `n`, a returned reborrow with a `defer` that uses `p` after
// the result exists, a returned loan of a local, a returned reborrow of a
// local's loan, and a record shorthand field in a returned record.
func TestReborrowConflictsNotApplied(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"both":           reborrowNode + "Both(x: &Node, y: mut Node): int {\n    return x.v + y.v\n}\n\nUse(p: mut Node): int {\n    return Both(p, p)\n}\n",
		"heldthenmut":    reborrowNode + "Bump(n: mut Node): int {\n    q: &Node = n\n    n.v += 1\n    return q.v\n}\n",
		"defer":          reborrowNode + "Node.Bump(mut self) {\n    self.v += 1\n}\n\nGet(p: mut Node): &Node {\n    defer p.Bump()\n    return p\n}\n",
		"localloan":      reborrowNode + "Make(): &Node {\n    var t := Node(v: 1)\n    return mut t\n}\n",
		"localreborrow":  reborrowNode + "Make(): &Node {\n    var t := Node(v: 1)\n    p := mut t\n    return p\n}\n",
		"returnedrecord": reborrowNode + "Take(n: mut Node): (n: &Node, k: int) from n {\n    k := 1\n    return (n, k)\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := reborrowDiag(t, src)
			for _, f := range d.Fixes {
				if f.Class == diag.Proven {
					t.Fatalf("proven fix %+v", f)
				}
			}
			file := filepath.Join(t.TempDir(), "main.mk")
			if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.Fix(FixOptions{})
			if err != nil {
				t.Fatal(err)
			}
			out, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != src || res.Stopped != "no proven fix" {
				t.Errorf("mk fix stopped %q and wrote:\n%s", res.Stopped, out)
			}
		})
	}
}

// TestReborrowNoFix: a `mut T` met by a `T` gets no fix and no try line, and
// nested parentheses get one fix, on the expression inside them.
func TestReborrowNoFix(t *testing.T) {
	t.Parallel()
	r := checkText(t, reborrowNode+"Copy(n: mut Node): Node {\n    return n\n}\n")
	if len(r.Report.Diags) != 1 {
		t.Fatalf("mut T to T: want one E0301:\n%s", diagLines(r))
	}
	if d := r.Report.Diags[0].Diag; d.Code != diag.E0301 || len(d.Fixes) != 0 || len(d.Alternatives) != 0 {
		t.Errorf("mut T to T: got %v %q fixes %+v alternatives %q", d.Code, d.Message, d.Fixes, d.Alternatives)
	}
	src := reborrowNode + "Same(n: mut Node): mut Node from n {\n    return n\n}\n\nPass(n: mut Node): int {\n    return Read((Same(n)))\n}\n"
	r = checkText(t, src)
	fixes := 0
	for _, e := range r.Report.Diags {
		fixes += len(e.Diag.Fixes)
		for _, f := range e.Diag.Fixes {
			if out := applyFix(src, f); !strings.Contains(out, "Read((&*Same(n)))") {
				t.Errorf("nested parentheses: applied text:\n%s", out)
			}
		}
	}
	if fixes != 1 {
		t.Errorf("nested parentheses: want one fix, got %d:\n%s", fixes, diagLines(r))
	}
}
