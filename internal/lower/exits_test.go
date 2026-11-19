// Why: where temporaries end, which cleanups run on each exit edge, and the
// order a scope block exits in decide what borrow and codegen see.
// What: TestR21_* (onerr and defer), TestR14_* (temporaries),
// TestBreakFromBlockInsideHandler, TestR17_ExitOrder,
// TestForHeaderTemporaries.
// How: lowers small programs and reads the scope ends and cleanup copies in
// each block of the MIR.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
)

// resSrc declares a linear resource with a fallible Close and an Abandon,
// the shape of File and of the memory model's Save example.
const resSrc = `error E
linear struct Res {
	h: int
}
Res.Close(self): E!void {
	Res(h) := self
	if h < 0 {
		fail E
	}
}
Res.Abandon(self) {
	Res(h) := self
	Assert(h == h)
}
Open(): E!Res {
	return Res(h: 1)
}
Other(): E!void {
}
Log() {
}
`

// blocksOf returns the printed text of each block, by block ID.
func blockText(f string) map[string]string {
	out := map[string]string{}
	var cur string
	for _, line := range strings.Split(f, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "bb") && strings.HasSuffix(t, ":") {
			cur = strings.Fields(t)[0]
			cur = strings.TrimSuffix(cur, ":")
			continue
		}
		if cur != "" {
			out[cur] += t + "\n"
		}
	}
	return out
}

func TestR21_OnerrGuard(t *testing.T) {
	l := lowerSrc(t, resSrc+`Save(): E!void {
	var f := try Open()
	onerr f.Abandon()
	try Other()
	try f.Close()
}
`)
	b := l.body("Save")
	f := l.print("Save")
	// Two try edges after the onerr (Other's and Close's), each with a guard
	// on f before its Abandon copy; Open's edge precedes the onerr.
	guards := 0
	for _, blk := range b.Blocks {
		if blk.Term.Kind == mir.TermOnerrGuard {
			guards++
			if b.Blocks[blk.Term.Yes].Origin != mir.OriginOnerr {
				t.Fatalf("the guard's run edge is not the onerr copy:\n%s", f)
			}
		}
	}
	if guards != 2 {
		t.Fatalf("want 2 onerr guards (Other's and Close's try edges), got %d:\n%s", guards, f)
	}
	// Close's edge (a fallible call consumes f only on success, so f is
	// still owned there): the guard follows the call that moves f.
	wantIn(t, f, "call m.Res:Close(move _3)", "onerr_guard _3", "call m.Res:Abandon(move _3)")
	if count(f, "call m.Res:Abandon") != 2 {
		t.Fatalf("one Abandon copy per guarded edge:\n%s", f)
	}
}

func TestR21_OnerrGuardOnParameter(t *testing.T) {
	l := lowerSrc(t, resSrc+`Finish(f: Res): E!void {
	onerr f.Abandon()
	try Other()
	try f.Close()
	try Other()
}
`)
	f := l.print("Finish")
	if count(f, "onerr_guard _1") != 3 {
		t.Fatalf("a guard on the parameter _1 on each of the three try edges:\n%s", f)
	}
	if count(f, "call m.Res:Abandon(move _1)") != 3 {
		t.Fatalf("an Abandon copy per edge:\n%s", f)
	}
}

func TestR21_DeferInLoopEachIteration(t *testing.T) {
	l := lowerSrc(t, resSrc+`Sum(xs: []int): int {
	var s := 0
	for x in xs {
		defer Log()
		s += x
	}
	return s
}
`)
	b := l.body("Sum")
	f := l.print("Sum")
	// The body's fall-through to the latch is an exit of the body scope:
	// one copy there, and none on the function's return.
	if count(f, "call m.Log()") != 1 {
		t.Fatalf("one defer copy, before the latch:\n%s", f)
	}
	latch := mir.NoBlock
	for i, blk := range b.Blocks {
		if len(blk.Stmts) > 0 && blk.Stmts[0].Rvalue.Bin == mir.AddNoWrap { // the counter latch
			latch = mir.BlockID(i)
		}
	}
	if latch == mir.NoBlock {
		t.Fatalf("no latch:\n%s", f)
	}
	// The copy's end reaches the latch.
	reaches := false
	for i, blk := range b.Blocks {
		if blk.Origin == mir.OriginDefer {
			for _, s := range b.Successors(mir.BlockID(i), nil) {
				for _, s2 := range b.Successors(s, nil) {
					if s2 == latch {
						reaches = true
					}
				}
			}
		}
	}
	if !reaches {
		t.Fatalf("the defer copy does not lead to the latch:\n%s", f)
	}
}

func TestR14_TooLongShape(t *testing.T) {
	l := lowerSrc(t, `struct P {
	x: int
	y: int
}
MakeP(): P {
	return P(x: 1, y: 2)
}
TooLong(): int {
	r := &MakeP()
	return r.x
}
`)
	// MM TooLong: the temporary ends right after the binding, while r still
	// holds its loan; borrow reports the dead temporary's loan.
	wantIn(t, l.print("TooLong"), "= call m.MakeP()", "_2 = &_1", "scope_end _1", "copy (*_2).0")
}

func TestR14_IfMatchWholeConstruct(t *testing.T) {
	l := lowerSrc(t, `struct P {
	x: int
	y: int
}
MakeP(): P {
	return P(x: 1, y: 2)
}
Use(p: &P): int {
	return p.x
}
F(o: ?int): int {
	if Use(&MakeP()) > 0 {
		return 1
	}
	match o {
		some(v) {
			return v
		}
		none {
			return 0
		}
	}
}
`)
	f := l.print("F")
	bt := blockText(f)
	// The condition's temporaries (_2 the P, _3 its borrow) end on the
	// early return inside the then-branch and after the join, never before
	// the branch runs.
	ends := 0
	for _, txt := range bt {
		if strings.Contains(txt, "scope_end _2") {
			ends++
			if !strings.Contains(txt, "scope_end _3") || strings.Index(txt, "scope_end _3") > strings.Index(txt, "scope_end _2") {
				t.Fatalf("borrow ends before its temporary, in reverse creation order:\n%s", f)
			}
		}
	}
	if ends != 2 {
		t.Fatalf("condition temporaries end on the early return and after the join (2 sites), got %d:\n%s", ends, f)
	}
	// The scrutinee temporary _6 ends in each arm, after the arm's locals.
	if count(f, "scope_end _6") != 2 {
		t.Fatalf("the scrutinee temporary ends on both arms' exits:\n%s", f)
	}
	wantIn(t, f, "scope_end _8", "scope_end _7", "scope_end _6")
}

func TestR14_LinearTempAtTryEdge(t *testing.T) {
	l := lowerSrc(t, resSrc+`Acquire(): Res {
	return Res(h: 2)
}
Two(): E!int {
	return 1
}
Take2(a: Res, n: int): E!void {
	onerr a.Abandon()
	try a.Close()
	Assert(n == n)
}
F(): E!void {
	try Take2(Acquire(), try Two())
}
`)
	f := l.print("F")
	bt := blockText(f)
	// Two's try edge leaves the statement while the Acquire temporary is
	// live: its scope end is on that edge, so borrow sees the
	// linear temporary end unconsumed there (E0402).
	found := false
	for _, txt := range bt {
		if strings.Contains(txt, "return try") && strings.Contains(txt, "scope_end _1") && !strings.Contains(txt, "Take2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no scope end of the Acquire temporary on Two's try edge:\n%s", f)
	}
}

func TestBreakFromBlockInsideHandler(t *testing.T) {
	l := lowerSrc(t, `error E
F(x: int): E!int {
	return x
}
Log() {
}
G(xs: []int, c: bool): int {
	var s := 0
	for x in xs {
		v := F(x) else {
			if c {
				onerr Log()
				break
			}
			return 0
		}
		s += v
	}
	return s
}
`)
	b := l.body("G")
	f := l.print("G")
	// The break leaves the if block, the else handler and the loop body:
	// an error exit for the if block, so Log's onerr copy runs, and the
	// goto carries ElseExit.
	if count(f, "call m.Log()") != 1 {
		t.Fatalf("one Log copy, on the break:\n%s", f)
	}
	elseExits := 0
	for _, blk := range b.Blocks {
		if blk.Term.Kind == mir.TermGoto && blk.Term.ElseExit {
			elseExits++
		}
	}
	if elseExits != 1 {
		t.Fatalf("one ElseExit goto (the break), got %d:\n%s", elseExits, f)
	}
	wantAll(t, f, "call m.Log()", "goto bb4 else-exit")
}

func TestR17_ExitOrder(t *testing.T) {
	l := lowerSrc(t, resSrc+`Work(io: Io) {
}
Par(io: Io, xs: []int): E!int {
	var n := 0
	for x in xs {
		scope s := io.Scope() {
			defer Log()
			k := x + 1
			s.go Work(io)
			if k == 3 {
				return k
			}
			if k == 4 {
				break
			}
			try Other()
			n += k
		}
	}
	return n
}
`)
	b := l.body("Par")
	f := l.print("Par")
	// On every path that runs a Log copy, the order is: the copy, then the
	// StJoin, then the scope ends of the block's locals (k).
	joins := 0
	for i, blk := range b.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind == mir.StJoin {
				joins++
				// The block holding the join is entered from a copy's end.
				pred := false
				for j, blk2 := range b.Blocks {
					if blk2.Origin == mir.OriginDefer {
						for _, s := range b.Successors(mir.BlockID(j), nil) {
							if s == mir.BlockID(i) {
								pred = true
							}
						}
					}
				}
				if !pred {
					t.Fatalf("bb%d joins without a defer copy before it:\n%s", i, f)
				}
			}
		}
	}
	// Fall-through, return, break and try: four exits, four joins, the
	// fall-through's not canceled.
	if joins != 4 {
		t.Fatalf("want 4 joins (fall-through, return, break, try), got %d:\n%s", joins, f)
	}
	if count(f, "join _") != 4 || count(f, "cancel") != 3 {
		t.Fatalf("three early exits cancel, the fall-through does not:\n%s", f)
	}
	for _, txt := range blockText(f) {
		if j := strings.Index(txt, "join _"); j >= 0 {
			if e := strings.Index(txt, "scope_end"); e >= 0 && e < j {
				t.Fatalf("a scope end before the join:\n%s", txt)
			}
		}
	}
}

// TestForHeaderTemporaries pins temporaries for the for forms whose
// header runs once: the value iterated over (and a range's bounds) lives to
// the loop's exit; every other temporary of the header ends before the
// loop's header block, so a holder that keeps a loan on one through the
// loop meets its scope end (E0506, borrow's).
func TestForHeaderTemporaries(t *testing.T) {
	l := lowerSrc(t, `struct S {
	n: int
}
Count(s: &S): int {
	return s.n
}
View(s: &[]int): []int {
	return *s
}
MakeArr(): [3]int {
	return [1, 2, 3]
}
A(xs: []int): int {
	var t := 0
	for x in View(&xs) {
		t += x
	}
	return t
}
B(): int {
	var t := 0
	for x in &MakeArr() {
		t += *x
	}
	return t
}
C(s: S): int {
	var t := 0
	for i in 0..Count(&s) {
		t += i
	}
	return t
}
`)
	type ends struct {
		b      *mir.Body
		blocks map[mir.LocalID][]mir.BlockID
	}
	endsOf := func(key string) ends {
		b := l.body(key)
		e := ends{b: b, blocks: map[mir.LocalID][]mir.BlockID{}}
		for i, blk := range b.Blocks {
			for _, st := range blk.Stmts {
				if st.Kind == mir.StScopeEnd {
					e.blocks[st.Local] = append(e.blocks[st.Local], mir.BlockID(i))
				}
			}
		}
		return e
	}
	// dests are the locals assigned by an RvRef or RvSlice, or by a call
	// for RvUse, in block order.
	dests := func(b *mir.Body, kind mir.RvalueKind) []mir.LocalID {
		var out []mir.LocalID
		for _, blk := range b.Blocks {
			for _, st := range blk.Stmts {
				if kind != mir.RvUse && st.Kind == mir.StAssign && st.Rvalue.Kind == kind {
					out = append(out, st.Place.Local)
				}
			}
			if kind == mir.RvUse && blk.Term.Kind == mir.TermCall {
				out = append(out, blk.Term.Dest.Local)
			}
		}
		return out
	}
	before := func(key string, e ends, l mir.LocalID) {
		t.Helper()
		bs := e.blocks[l]
		h := e.b.Loops[0].Header
		if len(bs) != 1 || e.b.Blocks[bs[0]].Term.Kind != mir.TermGoto || e.b.Blocks[bs[0]].Term.Target != h {
			t.Errorf("%s: _%d ends in %v, want once, in the block that enters the header bb%d", key, l, bs, h)
		}
	}
	atExit := func(key string, e ends, l mir.LocalID) {
		t.Helper()
		bs := e.blocks[l]
		if len(bs) != 1 || bs[0] != e.b.Loops[0].Exit {
			t.Errorf("%s: _%d ends in %v, want once, at the exit bb%d", key, l, bs, e.b.Loops[0].Exit)
		}
	}
	// A: the &xs temporary ends before the loop, View's result at the exit.
	a := endsOf("A")
	before("A", a, dests(a.b, mir.RvRef)[0])
	atExit("A", a, dests(a.b, mir.RvUse)[0])
	// B: the array MakeArr returns is the value the &-view borrows: both
	// end at the exit.
	bb := endsOf("B")
	atExit("B", bb, dests(bb.b, mir.RvUse)[0])
	atExit("B", bb, dests(bb.b, mir.RvSlice)[0])
	// C: &s and Count's result end before the loop; the bound and the
	// counter (the two locals of the header's Lt) at the exit.
	c := endsOf("C")
	before("C", c, dests(c.b, mir.RvRef)[0])
	before("C", c, dests(c.b, mir.RvUse)[0])
	hdr := c.b.Blocks[c.b.Loops[0].Header]
	if len(hdr.Stmts) == 0 || hdr.Stmts[0].Rvalue.Kind != mir.RvBinary {
		t.Fatalf("C: header has no comparison:\n%s", l.print("C"))
	}
	cmp := hdr.Stmts[0].Rvalue
	atExit("C", c, cmp.A.Place.Local)
	atExit("C", c, cmp.B.Place.Local)
	if t.Failed() {
		t.Logf("A:\n%s\nB:\n%s\nC:\n%s", l.print("A"), l.print("B"), l.print("C"))
	}
}
