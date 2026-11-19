// Why: defects found by dogfood programs in the front end must stay fixed.
// What: regression tests of index evaluation before a List borrow, an
// all-wildcard destructure of a linear view, and an http.Client in a task.
// How: each test runs a small program through the whole front end (check,
// lower, borrow) and compares the diagnostics.

package driver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestImplementerIndexBeforeBorrow: the index of a List
// field runs before ItemsMut borrows the field, so a method call on
// self in the index is no E0504, as over an array field.
func TestImplementerIndexBeforeBorrow(t *testing.T) {
	t.Parallel()
	r := checkText(t, `struct Counts {
    slots: List[int]
    fixed: [4]int
    slot: int
}

Counts.Pick(&self): int {
    return self.slot
}

Counts.Bump(mut self) {
    self.slots[self.Pick()] += 1
    self.fixed[self.Pick()] += 1
    x := self.slots[self.Pick()]
    self.slots[self.Pick()] = x
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var c := Counts(slots: List[int].New(sys.arena), fixed: [0, 0, 0, 0], slot: 0)
    try c.slots.Push(0)
    c.Bump()
    try sys.Print("{n}\n", (n: c.slots[0]))
}
`)
	if len(r.Report.Diags) != 0 {
		t.Fatalf("want a clean check:\n%s", diagLines(r))
	}
}

// TestIndexMayWriteBeforeBorrow: the index runs to completion
// before the List is borrowed, so an index that writes the List checks as
// it does over an array; a view of the List live across the statement is
// still a conflict.
func TestIndexMayWriteBeforeBorrow(t *testing.T) {
	t.Parallel()
	r := checkText(t, `Grow(xs: mut List[int]): int {
    xs.Push(1) else { }
    return 0
}

GrowArray(xs: mut [2]int): int {
    xs[1] = 5
    return 0
}

F(a: &Arena): OutOfMemory!void {
    var xs := List[int].New(a)
    try xs.Push(0)
    xs[Grow(mut xs)] += 1
    var ys: [2]int = [0, 0]
    ys[GrowArray(mut ys)] += 1
}
`)
	if len(r.Report.Diags) != 0 {
		t.Fatalf("want a clean check:\n%s", diagLines(r))
	}
	r = checkText(t, `Grow(xs: mut List[int]): int {
    xs.Push(1) else { }
    return 0
}

F(a: &Arena): OutOfMemory!int {
    var xs := List[int].New(a)
    try xs.Push(0)
    v := xs.Items()
    xs[Grow(mut xs)] += 1
    return v[0]
}
`)
	if len(r.Report.Diags) == 0 {
		t.Fatal("want a borrow diagnostic: v views xs across the write")
	}
}

// TestDestructureAllWildcardNonPlain: a destructure of a linear view
// struct whose every leaf is `_` and none plain was an internal compiler
// error in lower; it checks clean.
func TestDestructureAllWildcardNonPlain(t *testing.T) {
	t.Parallel()
	r := checkText(t, `linear view struct Enc {
    s: mut []u8
    hist: mut []u8
}

Enc.Abandon(self) {
    Enc(s: _, hist: _) := self
}

export Main(sys: &System) {
    var a: [4]u8 = [0, 0, 0, 0]
    var b: [4]u8 = [0, 0, 0, 0]
    e := Enc(s: mut a[..], hist: mut b[..])
    e.Abandon()
}
`)
	if len(r.Report.Diags) != 0 {
		t.Fatalf("want a clean check:\n%s", diagLines(r))
	}
}

// TestClientInTaskChecks: the repro
// (tests/programs/httploop/repro/client-in-task.mk.txt), a task that makes
// and uses an http.Client, checks clean, twice over one cache. mk check of
// it overflowed the stack in the facts walk on std.http's Client.slots
// (Pair nested four deep) when those facts were asked top down. In process
// the order differs and this test passes without the fix too, so the
// regression test proper is internal/check's
// TestFetchFactsTopDownTerminates; this one keeps the repro whole.
func TestClientInTaskChecks(t *testing.T) {
	t.Parallel()
	src := `import std.http
import std.url

Visit(io: Io, h: &Heap, port: int, status: mut int) {
    var a := Arena.New(h) else {
        return
    }
    defer a.Free()
    client := http.Client.New(h, http.DefaultClientLimits)
    defer client.Close()
    text := a.Fmt("http://127.0.0.1:{port}/hello", (port: port)) else {
        return
    }
    u := url.Parse(&a, text, none) else {
        return
    }
    hs := List[http.Header].New(&a).Done()
    req := http.ClientRequest(method: "GET", url: &u, headers: hs[..], body: "".AsBytes())
    reply := client.Fetch(&a, io, &req) else {
        return
    }
    *status = reply.status
}

export Main(sys: &System): WriteError!void {
    var status := 0
    scope s := sys.io.Scope() {
        s.go Visit(sys.io, sys.heap, 1, mut status)
    }
    try sys.Print("{status}\n", (status: status))
}
`
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	// The second check reads std's results from the cache the first wrote,
	// so the facts of http.Client are asked top down.
	for range 2 {
		s, err := NewSession(Config{Root: file, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		if r := s.Check(); len(r.Report.Diags) != 0 {
			t.Fatalf("want a clean check:\n%s", diagLines(r))
		}
	}
}
