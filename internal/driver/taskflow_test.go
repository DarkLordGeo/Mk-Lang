// Why: a task's clauses are applied again after stores while it runs, and
// a body with many tasks and many stores must not make that cubic.
// What: TestTaskFlowWork, a chain of Forward tasks spawned in reverse order
// and fed N values, checked with a bound on the clause applications.
// How: generates the program, checks it, and reads
// borrow.FlowApplications before and after; a count, not a clock.

package driver

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/borrow"
)

// taskChain is N Forward tasks from c[i] into c[i+1], spawned from the last
// to the first, then N sends of distinct locals into c[0].
func taskChain(n int) string {
	var b strings.Builder
	b.WriteString(`Forward(a: &Chan[&i32], io: Io, b: &Chan[&i32]) *a into b {
    v := a.Recv(io) else {
        return
    }
    b.Send(io, v) else {}
}

Go(sys: &System, out: &Chan[&i32]): OutOfMemory!void {
`)
	for i := range n {
		fmt.Fprintf(&b, "    x%d: i32 = %d\n", i, i)
	}
	for i := range n {
		fmt.Fprintf(&b, "    c%d := try Chan[&i32].New(sys.heap.Alloc(), 1)\n", i)
	}
	b.WriteString("    scope s := sys.io.Scope() {\n")
	for i := n - 1; i >= 0; i-- {
		next := "out"
		if i+1 < n {
			next = fmt.Sprintf("&c%d", i+1)
		}
		fmt.Fprintf(&b, "        s.go Forward(&c%d, sys.io, %s)\n", i, next)
	}
	for i := range n {
		fmt.Fprintf(&b, "        c0.Send(sys.io, &x%d) else {}\n", i)
	}
	b.WriteString("    }\n")
	for i := range n {
		fmt.Fprintf(&b, "    c%d.Free(sys.heap.Alloc())\n", i)
	}
	b.WriteString("}\n")
	return b.String()
}

// TestTaskFlowWork checks a chain of 200 tasks: every send reaches out, so
// each is E0505, and the clause applications stay within a bound linear in
// tasks times stores per solve round (the worklist applies only the flows
// whose region a store touched), far below the cubic count of applying every
// flow to a fixpoint after every store.
func TestTaskFlowWork(t *testing.T) {
	const n = 200
	before := borrow.FlowApplications()
	r := checkText(t, taskChain(n))
	work := borrow.FlowApplications() - before
	e0505 := 0
	for _, e := range r.Report.Diags {
		if e.Diag.Code.String() == "E0505" {
			e0505++
		}
	}
	if e0505 == 0 {
		t.Errorf("no E0505: the sends into the chain must reach out")
	}
	// Per solve round and the replay: n seed applications at the spawns, and
	// up to 2n applications per send (the chain, and each flow once more for
	// the root its own store grew); a few rounds.
	if limit := uint64(8 * n * (2*n + 1)); work > limit {
		t.Errorf("%d clause applications for %d tasks and %d sends, want at most %d", work, n, n, limit)
	}
	t.Logf("%d clause applications", work)
}
