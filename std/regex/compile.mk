// Why: every matcher of std.regex (DFA, Pike VM, backtracker) runs one byte
// program, so the AST must be compiled once into a small one, forward and
// reversed.
//
// What: CompileTree, Prog and Inst with the opcodes OpByte, OpSparse,
// OpSplit, OpSave, OpAssert, OpMatch, OpFail and OpNop, and the compiler
// state Comp with its fragments (Frag, whose dangling exits Hole encodes).
//
// How: Thompson's construction (Thompson 1968; Cox 2007) with fragments
// whose dangling exits are threaded through their own fields (Cox's patch
// lists); repeats expanded as Go's syntax.Simplify and compile.go do (x* of
// a nullable x is (x+)?, Go issue 46123); classes compiled to a minimal DAG
// of byte-range states over UTF-8: scalar ranges split into byte-range
// sequences (as Rust's regex-syntax utf8.rs splits them), a trie over the
// sorted sequences, and equal states shared bottom up (Daciuk et al. 2000),
// so `.` is 8 states. With rev, the program of the reversed pattern, which
// the reverse DFA runs backward from a match's end to find its start (RE2's
// and Rust's reverse search): concatenations in reverse order, \A and \z, ^
// and $ swapped, no capture slots, and each class as the minimal
// deterministic automaton of its UTF-8 sequences read last byte first (the
// forward DAG reversed and determinized, Brzozowski 1962, the job RE2's
// suffix cache and Rust's RangeTrie do), so a reversed `\pL` is about as
// small as the forward one.

// Opcodes of Inst.op.
const OpByte: u8 = 1
const OpSparse: u8 = 2
const OpSplit: u8 = 3
const OpSave: u8 = 4
const OpAssert: u8 = 5
const OpNop: u8 = 6
const OpMatch: u8 = 7
const OpFail: u8 = 8

// MaxInsts bounds a program: the Pike VM's thread lists are stack arrays
// of at most this many entries.
const MaxInsts: int = 4096
const MaxEdges: int = 65536

// NoHole ends a hole chain; ToEnd marks a class's exits while it is built.
const NoHole: u32 = 0xFFFFFFFF
const ToEnd: u32 = 0xFFFFFFFE

// Inst is one instruction. OpByte: a byte in lo .. hi goes to x. OpSparse:
// edges x .. x + y, sorted and disjoint. OpSplit: x, then y. OpSave: slot
// y, then x. OpAssert: kind lo, then x. OpNop: x.
struct Inst {
    op: u8
    lo: u8
    hi: u8
    x: u32
    y: u32
}

struct Edge {
    lo: u8
    hi: u8
    next: u32
}

// Frag is a compiled piece: its entry, the chain of its unfilled exits
// (each hole's field holds the next hole; the tail's holds NoHole), and
// whether it can match the empty string. A hole is index << 2 | k, with k
// 0 for Inst.x, 1 for Inst.y and 2 for Edge.next.
struct Frag {
    start: u32
    head: u32
    tail: u32
    empty: bool
}

// Seq is one UTF-8 byte-range sequence: byte j in lo[j] .. hi[j].
struct Seq {
    n: int
    lo: [4]u8
    hi: [4]u8
}

struct Prog {
    insts: []Inst
    edges: []Edge
    start: u32
}

// RSet is a state of ClassRev's subset construction: the forward
// positions pool[off .. off + n], ascending, their hash, and its
// transitions rout[eo .. eo + en] (Edge.next a subset index).
struct RSet {
    off: int
    n: int
    h: u32
    eo: int
    en: int
}

struct Comp {
    insts: List[Inst]
    edges: List[Edge]
    seqs: List[Seq]
    split: List[Rr]
    pend: List[Edge]
    rin: List[Edge]
    rhead: List[int]
    rpool: List[u32]
    rsets: List[RSet]
    rout: List[Edge]
    rcand: List[Edge]
    rcur: List[u32]
    rid: List[u32]
    rtab: List[u32]
    nodes: []Node
    kids: []int
    ranges: []Rr
    rev: bool
}

CompileTree(a: &Arena, t: Tree, rev: bool): Error!Prog from a, t {
    var c := Comp(insts: List[Inst].New(a), edges: List[Edge].New(a), seqs: List[Seq].New(a), split: List[Rr].New(a), pend: List[Edge].New(a), rin: List[Edge].New(a), rhead: List[int].New(a), rpool: List[u32].New(a), rsets: List[RSet].New(a), rout: List[Edge].New(a), rcand: List[Edge].New(a), rcur: List[u32].New(a), rid: List[u32].New(a), rtab: List[u32].New(a), nodes: t.nodes, kids: t.kids, ranges: t.ranges, rev: rev)
    f := try c.Node(t.root)
    m := try c.Emit(OpMatch, 0, 0, 0, 0)
    c.Patch(f, m)
    Comp(insts, edges, seqs: _, split: _, pend: _, rin: _, rhead: _, rpool: _, rsets: _, rout: _, rcand: _, rcur: _, rid: _, rtab: _, ..) := c
    return Prog(insts: insts.Done()[..], edges: edges.Done()[..], start: f.start)
}

Hole(i: u32, k: u32): u32 {
    return i << 2 | k
}

Comp.Emit(mut self, op: u8, lo: u8, hi: u8, x: u32, y: u32): Error!u32 {
    if self.insts.Len() >= MaxInsts {
        fail TooLarge
    }
    try self.insts.Push(Inst(op: op, lo: lo, hi: hi, x: x, y: y))
    return Trunc[u32](self.insts.Len() - 1)
}

Comp.Get(&self, h: u32): u32 {
    i := Cast[int](h >> 2)
    k := h & 3
    if k == 0 {
        return self.insts[i].x
    }
    if k == 1 {
        return self.insts[i].y
    }
    return self.edges[i].next
}

Comp.Set(mut self, h: u32, v: u32) {
    i := Cast[int](h >> 2)
    k := h & 3
    if k == 0 {
        self.insts[i].x = v
    } else if k == 1 {
        self.insts[i].y = v
    } else {
        self.edges[i].next = v
    }
}

// Patch points every exit of f at to.
Comp.Patch(mut self, f: Frag, to: u32) {
    var h := f.head
    for h != NoHole {
        next := self.Get(h)
        self.Set(h, to)
        h = next
    }
}

// Join is f's and g's exits as one chain.
Comp.Join(mut self, f: Frag, g: Frag): (head: u32, tail: u32) {
    if f.head == NoHole {
        return (head: g.head, tail: g.tail)
    }
    if g.head == NoHole {
        return (head: f.head, tail: f.tail)
    }
    self.Set(f.tail, g.head)
    return (head: f.head, tail: g.tail)
}

// One is the fragment of instruction i with one exit, field k.
One(i: u32, k: u32, empty: bool): Frag {
    h := Hole(i, k)
    return Frag(start: i, head: h, tail: h, empty: empty)
}

Comp.Node(mut self, i: int): Error!Frag {
    nd := self.nodes[i]
    k := nd.kind
    if k == KEmpty {
        return One(try self.Emit(OpNop, 0, 0, NoHole, 0), 0, true)
    }
    if k == KLit {
        var one: [1]Rr = [Rr(lo: Cast[u32](nd.a), hi: Cast[u32](nd.a))]
        return try self.Class(one[..])
    }
    if k == KClass {
        return try self.Class(self.ranges[nd.a..nd.a + nd.b])
    }
    if k == KAssert {
        kind := if self.rev { Mirrored(Cast[u8](nd.a)) } else { Cast[u8](nd.a) }
        return One(try self.Emit(OpAssert, kind, 0, NoHole, 0), 0, true)
    }
    if k == KCap && self.rev {
        return try self.Node(nd.a)
    }
    if k == KCap {
        s := try self.Emit(OpSave, 0, 0, 0, Cast[u32](2 * nd.b))
        f := try self.Node(nd.a)
        self.insts[Cast[int](s)].x = f.start
        e := try self.Emit(OpSave, 0, 0, NoHole, Cast[u32](2 * nd.b + 1))
        self.Patch(f, e)
        return Frag(start: s, head: Hole(e, 0), tail: Hole(e, 0), empty: f.empty)
    }
    if k == KCat && self.rev {
        var f := try self.Node(self.kids[nd.a + nd.b - 1])
        var j := nd.a + nd.b - 2
        for j >= nd.a {
            g := try self.Node(self.kids[j])
            f = self.Cat(f, g)
            j -= 1
        }
        return f
    }
    if k == KCat {
        var f := try self.Node(self.kids[nd.a])
        for j in nd.a + 1..nd.a + nd.b {
            g := try self.Node(self.kids[j])
            f = self.Cat(f, g)
        }
        return f
    }
    if k == KAlt {
        return try self.Alt(nd.a, nd.b)
    }
    return try self.Rep(nd.a, nd.b, nd.c, nd.flags & NGreedy != 0)
}

// Mirrored is assertion kind as the reversed text sees it.
Mirrored(kind: u8): u8 {
    if kind == ABeginText {
        return AEndText
    }
    if kind == AEndText {
        return ABeginText
    }
    if kind == ABeginLine {
        return AEndLine
    }
    if kind == AEndLine {
        return ABeginLine
    }
    return kind
}

Comp.Cat(mut self, f: Frag, g: Frag): Frag {
    self.Patch(f, g.start)
    return Frag(start: f.start, head: g.head, tail: g.tail, empty: f.empty && g.empty)
}

// Alt compiles kids first .. first + n as Split(k1, Split(k2, ...)).
Comp.Alt(mut self, first: int, n: int): Error!Frag {
    var acc := try self.Node(self.kids[first + n - 1])
    var j := first + n - 2
    for j >= first {
        f := try self.Node(self.kids[j])
        s := try self.Emit(OpSplit, 0, 0, f.start, acc.start)
        h := self.Join(f, acc)
        acc = Frag(start: s, head: h.head, tail: h.tail, empty: f.empty || acc.empty)
        j -= 1
    }
    return acc
}

// Rep compiles child{min,max} (max -1: unbounded) as Go's simplify.go:
// x{0} is empty, x{0,} is x*, x{1,} is x+, x{n,} is n - 1 copies then x+,
// x{n,m} is n copies then m - n nested optionals, x(x(x)?)?.
Comp.Rep(mut self, child: int, min: int, max: int, greedy: bool): Error!Frag {
    if min == 0 && max == 0 {
        return One(try self.Emit(OpNop, 0, 0, NoHole, 0), 0, true)
    }
    if max < 0 {
        if min == 0 {
            f := try self.Node(child)
            return try self.Star(f, greedy)
        }
        if min == 1 {
            f := try self.Node(child)
            return try self.Plus(f, greedy)
        }
        pre := try self.Copies(child, min - 1)
        g := try self.Node(child)
        p := try self.Plus(g, greedy)
        return self.Cat(pre, p)
    }
    if max == min {
        return try self.Copies(child, min)
    }
    var suffix := try self.Quest(try self.Node(child), greedy)
    for _ in min + 1..max {
        x := try self.Node(child)
        suffix = try self.Quest(self.Cat(x, suffix), greedy)
    }
    if min == 0 {
        return suffix
    }
    pre := try self.Copies(child, min)
    return self.Cat(pre, suffix)
}

// Copies compiles n >= 1 copies of child, concatenated.
Comp.Copies(mut self, child: int, n: int): Error!Frag {
    var f := try self.Node(child)
    for _ in 1..n {
        g := try self.Node(child)
        f = self.Cat(f, g)
    }
    return f
}

// Loop is Split(f, exit) with f's exits back to it; lazy prefers the exit.
Comp.Loop(mut self, f: Frag, greedy: bool): Error!Frag {
    if greedy {
        l := try self.Emit(OpSplit, 0, 0, f.start, NoHole)
        self.Patch(f, l)
        return One(l, 1, true)
    }
    l := try self.Emit(OpSplit, 0, 0, NoHole, f.start)
    self.Patch(f, l)
    return One(l, 0, true)
}

Comp.Plus(mut self, f: Frag, greedy: bool): Error!Frag {
    l := try self.Loop(f, greedy)
    return Frag(start: f.start, head: l.head, tail: l.tail, empty: f.empty)
}

Comp.Star(mut self, f: Frag, greedy: bool): Error!Frag {
    if f.empty {
        p := try self.Plus(f, greedy)
        return try self.Quest(p, greedy)
    }
    return try self.Loop(f, greedy)
}

Comp.Quest(mut self, f: Frag, greedy: bool): Error!Frag {
    var q: u32
    var e: Frag
    if greedy {
        q = try self.Emit(OpSplit, 0, 0, f.start, NoHole)
        e = One(q, 1, true)
    } else {
        q = try self.Emit(OpSplit, 0, 0, NoHole, f.start)
        e = One(q, 0, true)
    }
    h := self.Join(e, f)
    return Frag(start: q, head: h.head, tail: h.tail, empty: true)
}

// Class compiles the sorted, merged scalar ranges rs: an empty class (or
// one of surrogates only) is OpFail; else the UTF-8 DAG, its exits chained.
Comp.Class(mut self, rs: []Rr): Error!Frag {
    try self.Utf8Seqs(rs)
    if self.seqs.Len() == 0 {
        i := try self.Emit(OpFail, 0, 0, 0, 0)
        return Frag(start: i, head: NoHole, tail: NoHole, empty: false)
    }
    i0 := self.insts.Len()
    e0 := self.edges.Len()
    var start := try self.Build(0, self.seqs.Len(), 0, i0)
    if self.rev {
        start = try self.ClassRev(i0, e0, start)
    }
    return self.Exits(i0, e0, start)
}

// Exits is the class fragment at start whose states are insts i0.. and
// edges e0..: every field still ToEnd, chained in creation order.
Comp.Exits(mut self, i0: int, e0: int, start: u32): Frag {
    var head := NoHole
    var tail := NoHole
    for i in i0..self.insts.Len() {
        if self.insts[i].op == OpByte && self.insts[i].x == ToEnd {
            h := Hole(Trunc[u32](i), 0)
            self.insts[i].x = NoHole
            if tail == NoHole {
                head = h
            } else {
                self.Set(tail, h)
            }
            tail = h
        }
    }
    for i in e0..self.edges.Len() {
        if self.edges[i].next == ToEnd {
            h := Hole(Trunc[u32](i), 2)
            self.edges[i].next = NoHole
            if tail == NoHole {
                head = h
            } else {
                self.Set(tail, h)
            }
            tail = h
        }
    }
    return Frag(start: start, head: head, tail: tail, empty: false)
}

// ClassRev replaces the class's forward DAG (insts i0.., edges e0.., entry
// start) with the DAG of its sequences read last byte first, and returns that
// DAG's entry. The forward DAG is deterministic and each of its states is
// reachable, so the subset construction of its reverse is the minimal
// deterministic automaton of the reversed sequences (Brzozowski 1962). A subset
// holds forward positions (a state's offset from i0, or f for the class's end);
// the first subset is {f}; on a byte the next subset holds every state with an
// edge on that byte into the subset. Only the entry has edges on lead and ASCII
// bytes, and no edge enters it, so a subset that holds it is {entry}: the end
// of the reversed class, ToEnd. (As one Split chain of byte chains, the reverse
// of `\pL`'s 836 sequences would be about 3,700 instructions, whose closures
// spend the reverse DFA's work budget after 41 states.) Time O(S (E + 256 k))
// for S subsets, E forward edges and k members a subset; TooLarge past MaxInsts
// subsets, which costs only the reverse DFA (RevDfa).
Comp.ClassRev(mut self, i0: int, e0: int, start: u32): Error!u32 {
    n := self.insts.Len()
    f := n - i0
    // The forward edges by target position, rin[rhead[p] .. rhead[p + 1]],
    // each with next the position it leaves (a counting sort).
    self.rhead.Clear()
    for _ in 0..f + 2 {
        try self.rhead.Push(0)
    }
    for s in i0..n {
        for k in 0..self.OutN(s) {
            e := self.Out(s, k)
            self.rhead[DagPos(e.next, i0, f) + 1] += 1
        }
    }
    for p in 0..f + 1 {
        self.rhead[p + 1] += self.rhead[p]
    }
    self.rin.Clear()
    self.rid.Clear()
    for _ in 0..self.rhead[f + 1] {
        try self.rin.Push(Edge(lo: 0, hi: 0, next: 0))
    }
    for p in 0..f + 1 {
        try self.rid.Push(Trunc[u32](self.rhead[p]))
    }
    for s in i0..n {
        for k in 0..self.OutN(s) {
            e := self.Out(s, k)
            p := DagPos(e.next, i0, f)
            at := Cast[int](self.rid[p])
            self.rin[at] = Edge(lo: e.lo, hi: e.hi, next: Trunc[u32](s - i0))
            self.rid[p] = Trunc[u32](at + 1)
        }
    }
    // The subset construction, in creation order.
    self.rpool.Clear()
    self.rsets.Clear()
    self.rout.Clear()
    self.rtab.Clear()
    self.rcur.Clear()
    try self.rcur.Push(Trunc[u32](f))
    _ = try self.RLookup()
    entry := Trunc[u32](Cast[int](start) - i0)
    var k := 0
    for k < self.rsets.Len() {
        q := self.rsets[k]
        if !self.RHas(q, entry) {
            try self.RRow(k)
        } else if q.n != 1 {
            fail TooLarge
        }
        if self.rsets.Len() > MaxInsts {
            fail TooLarge
        }
        k += 1
    }
    // Drop the forward DAG, then emit one instruction per subset but
    // {entry}, in creation order, so subset 0 is the new entry.
    for self.insts.Len() > i0 {
        _ = self.insts.Pop()
    }
    for self.edges.Len() > e0 {
        _ = self.edges.Pop()
    }
    self.rid.Clear()
    var next := i0
    for r in 0..self.rsets.Len() {
        if self.RHas(self.rsets[r], entry) {
            try self.rid.Push(ToEnd)
        } else {
            try self.rid.Push(Trunc[u32](next))
            next += 1
        }
    }
    for r in 0..self.rsets.Len() {
        q := self.rsets[r]
        if self.RHas(q, entry) {
            continue
        }
        if q.en == 1 {
            e := self.rout[q.eo]
            to := self.rid[Cast[int](e.next)]
            _ = try self.Emit(OpByte, e.lo, e.hi, to, 0)
        } else {
            if self.edges.Len() + q.en > MaxEdges {
                fail TooLarge
            }
            first := self.edges.Len()
            for x in q.eo..q.eo + q.en {
                e := self.rout[x]
                to := self.rid[Cast[int](e.next)]
                try self.edges.Push(Edge(lo: e.lo, hi: e.hi, next: to))
            }
            _ = try self.Emit(OpSparse, 0, 0, Trunc[u32](first), Trunc[u32](q.en))
        }
    }
    return self.rid[0]
}

// DagPos is the forward position of DAG target t: its state's offset from
// i0, or f for the class's end.
DagPos(t: u32, i0: int, f: int): int {
    if t == ToEnd {
        return f
    }
    return Cast[int](t) - i0
}

// OutN is the number of edges of DAG state s (an OpByte or OpSparse).
Comp.OutN(&self, s: int): int {
    ins := self.insts[s]
    if ins.op == OpByte {
        return 1
    }
    return Cast[int](ins.y)
}

// Out is edge k of DAG state s.
Comp.Out(&self, s: int, k: int): Edge {
    ins := self.insts[s]
    if ins.op == OpByte {
        return Edge(lo: ins.lo, hi: ins.hi, next: ins.x)
    }
    return self.edges[Cast[int](ins.x) + k]
}

// RHas: subset q holds position p.
Comp.RHas(&self, q: RSet, p: u32): bool {
    for j in 0..q.n {
        if self.rpool[q.off + j] == p {
            return true
        }
    }
    return false
}

// RRow fills subset k's transitions: the bytes are cut at every boundary
// of the edges entering its members, each piece's subset is looked up,
// and adjacent pieces with one subset make one range.
Comp.RRow(mut self, k: int): OutOfMemory!void {
    q := self.rsets[k]
    var cut: [257]bool = [false; 257]
    self.rcand.Clear()
    for j in 0..q.n {
        p := Cast[int](self.rpool[q.off + j])
        for e in self.rhead[p]..self.rhead[p + 1] {
            ed := self.rin[e]
            try self.rcand.Push(ed)
            cut[Cast[int](ed.lo)] = true
            cut[Cast[int](ed.hi) + 1] = true
        }
    }
    eo := self.rout.Len()
    var prev := -1
    var x := 0
    for x < 256 {
        var y := x + 1
        for y < 256 && !cut[y] {
            y += 1
        }
        b := Trunc[u8](x)
        self.rcur.Clear()
        for c in 0..self.rcand.Len() {
            ed := self.rcand[c]
            if b >= ed.lo && b <= ed.hi {
                try self.RAdd(ed.next)
            }
        }
        id := if self.rcur.Len() > 0 { try self.RLookup() } else { -1 }
        if id >= 0 && id == prev {
            last := self.rout.Len() - 1
            self.rout[last].hi = Trunc[u8](y - 1)
        } else if id >= 0 {
            try self.rout.Push(Edge(lo: b, hi: Trunc[u8](y - 1), next: Trunc[u32](id)))
        }
        prev = id
        x = y
    }
    self.rsets[k].eo = eo
    self.rsets[k].en = self.rout.Len() - eo
}

// RAdd inserts p into rcur, kept ascending and without repeats.
Comp.RAdd(mut self, p: u32): OutOfMemory!void {
    var i := self.rcur.Len()
    for i > 0 && self.rcur[i - 1] > p {
        i -= 1
    }
    if i > 0 && self.rcur[i - 1] == p {
        return
    }
    try self.rcur.Push(p)
    var j := self.rcur.Len() - 1
    for j > i {
        self.rcur[j] = self.rcur[j - 1]
        j -= 1
    }
    self.rcur[i] = p
}

// RLookup is the index of the subset rcur holds, made if it is new:
// FNV-1a over its members into rtab, open addressing with linear probing,
// grown to keep it at most half full. O(n) a lookup on average.
Comp.RLookup(mut self): OutOfMemory!int {
    var h: u32 = 2166136261
    for p in &self.rcur {
        h = (h ^ *p) *% 16777619
    }
    if self.rtab.Len() < 2 * (self.rsets.Len() + 1) {
        try self.RGrow()
    }
    n := self.rcur.Len()
    mask := self.rtab.Len() - 1
    var i := Cast[int](h ^ (h >> 15)) & mask
    for {
        v := self.rtab[i]
        if v == 0 {
            break
        }
        k := Cast[int](v) - 1
        q := self.rsets[k]
        if q.h == h && q.n == n {
            var same := true
            for j in 0..n {
                if self.rpool[q.off + j] != self.rcur[j] {
                    same = false
                    break
                }
            }
            if same {
                return k
            }
        }
        i = (i + 1) & mask
    }
    off := self.rpool.Len()
    for p in &self.rcur {
        try self.rpool.Push(*p)
    }
    try self.rsets.Push(RSet(off: off, n: n, h: h, eo: 0, en: 0))
    self.rtab[i] = Trunc[u32](self.rsets.Len())
    return self.rsets.Len() - 1
}

// RGrow doubles rtab (64 slots at least) and puts every subset back.
Comp.RGrow(mut self): OutOfMemory!void {
    size := if 2 * self.rtab.Len() < 64 { 64 } else { 2 * self.rtab.Len() }
    self.rtab.Clear()
    for _ in 0..size {
        try self.rtab.Push(0)
    }
    mask := size - 1
    for k in 0..self.rsets.Len() {
        h := self.rsets[k].h
        var i := Cast[int](h ^ (h >> 15)) & mask
        for self.rtab[i] != 0 {
            i = (i + 1) & mask
        }
        self.rtab[i] = Trunc[u32](k + 1)
    }
}

// Build makes the state for seqs i .. j, which share their first d byte
// ranges: one edge per distinct range at depth d, to ToEnd when the
// sequences end there, else to the state of their rest. A state equal to
// one already made for this class (from inst i0 on) is reused.
Comp.Build(mut self, i: int, j: int, d: int, i0: int): Error!u32 {
    base := self.pend.Len()
    var k := i
    for k < j {
        lo := self.seqs[k].lo[d]
        hi := self.seqs[k].hi[d]
        var e := k + 1
        for e < j && self.seqs[e].lo[d] == lo && self.seqs[e].hi[d] == hi {
            e += 1
        }
        next := if self.seqs[k].n > d + 1 { try self.Build(k, e, d + 1, i0) } else { ToEnd }
        try self.pend.Push(Edge(lo: lo, hi: hi, next: next))
        k = e
    }
    n := self.pend.Len() - base
    for s in i0..self.insts.Len() {
        if self.SameState(s, base, n) {
            self.Drop(base)
            return Trunc[u32](s)
        }
    }
    st: u32 = if n == 1 {
        p := self.pend[base]
        try self.Emit(OpByte, p.lo, p.hi, p.next, 0)
    } else {
        if self.edges.Len() + n > MaxEdges {
            fail TooLarge
        }
        first := self.edges.Len()
        for x in base..base + n {
            try self.edges.Push(self.pend[x])
        }
        try self.Emit(OpSparse, 0, 0, Trunc[u32](first), Trunc[u32](n))
    }
    self.Drop(base)
    return st
}

Comp.Drop(mut self, base: int) {
    for self.pend.Len() > base {
        _ = self.pend.Pop()
    }
}

// SameState: instruction s has exactly the n edges pend[base..].
Comp.SameState(&self, s: int, base: int, n: int): bool {
    ins := self.insts[s]
    if ins.op == OpByte {
        if n != 1 {
            return false
        }
        p := self.pend[base]
        return ins.lo == p.lo && ins.hi == p.hi && ins.x == p.next
    }
    if ins.op != OpSparse || Cast[int](ins.y) != n {
        return false
    }
    f := Cast[int](ins.x)
    for x in 0..n {
        a := self.edges[f + x]
        p := self.pend[base + x]
        if a.lo != p.lo || a.hi != p.hi || a.next != p.next {
            return false
        }
    }
    return true
}

// Utf8Seqs fills seqs with the UTF-8 sequences of rs, in byte order, as
// Rust's Utf8Sequences: split around the surrogates, at the encoded-length
// boundaries, then until each range differs only in its last k bytes, each
// of which runs over the whole continuation range.
Comp.Utf8Seqs(mut self, rs: []Rr): OutOfMemory!void {
    self.seqs.Clear()
    for r in rs {
        try self.split.Push(r)
        for top := self.split.Pop() {
            var cur := top
            for {
                if cur.lo < 0xE000 && cur.hi > 0xD7FF {
                    try self.split.Push(Rr(lo: 0xE000, hi: cur.hi))
                    cur.hi = 0xD7FF
                    continue
                }
                if cur.lo > cur.hi {
                    break
                }
                if try self.SplitLen(mut cur) {
                    continue
                }
                if cur.hi < 0x80 {
                    var s := Seq(n: 1, lo: [0; 4], hi: [0; 4])
                    s.lo[0] = Trunc[u8](cur.lo)
                    s.hi[0] = Trunc[u8](cur.hi)
                    try self.seqs.Push(s)
                    break
                }
                if try self.SplitMask(mut cur) {
                    continue
                }
                a := Encode(cur.lo)
                b := Encode(cur.hi)
                var s := Seq(n: a.n, lo: [0; 4], hi: [0; 4])
                for x in 0..a.n {
                    s.lo[x] = a.b[x]
                    s.hi[x] = b.b[x]
                }
                try self.seqs.Push(s)
                break
            }
        }
    }
}

// SplitLen cuts cur at the first encoded-length boundary inside it,
// pushing the upper part; true when it cut.
Comp.SplitLen(mut self, cur: mut Rr): OutOfMemory!bool {
    var maxes: [3]u32 = [0x7F, 0x7FF, 0xFFFF]
    for m in maxes {
        if cur.lo <= m && m < cur.hi {
            try self.split.Push(Rr(lo: m + 1, hi: cur.hi))
            cur.hi = m
            return true
        }
    }
    return false
}

// SplitMask cuts cur where its last k bytes stop covering the whole
// continuation range, for k from 1 to 3; true when it cut.
Comp.SplitMask(mut self, cur: mut Rr): OutOfMemory!bool {
    for k in 1..4 {
        m := (Cast[u32](1) << Trunc[u32](6 * k)) - 1
        if cur.lo & ~m != cur.hi & ~m {
            if cur.lo & m != 0 {
                try self.split.Push(Rr(lo: (cur.lo | m) + 1, hi: cur.hi))
                cur.hi = cur.lo | m
                return true
            }
            if cur.hi & m != m {
                try self.split.Push(Rr(lo: cur.hi & ~m, hi: cur.hi))
                cur.hi = (cur.hi & ~m) - 1
                return true
            }
        }
    }
    return false
}

// Encode is the UTF-8 encoding of scalar c.
Encode(c: u32): (b: [4]u8, n: int) {
    var b: [4]u8 = [0; 4]
    if c < 0x80 {
        b[0] = Trunc[u8](c)
        return (b: b, n: 1)
    }
    if c < 0x800 {
        b[0] = Trunc[u8](0xC0 | c >> 6)
        b[1] = Trunc[u8](0x80 | c & 0x3F)
        return (b: b, n: 2)
    }
    if c < 0x10000 {
        b[0] = Trunc[u8](0xE0 | c >> 12)
        b[1] = Trunc[u8](0x80 | c >> 6 & 0x3F)
        b[2] = Trunc[u8](0x80 | c & 0x3F)
        return (b: b, n: 3)
    }
    b[0] = Trunc[u8](0xF0 | c >> 18)
    b[1] = Trunc[u8](0x80 | c >> 12 & 0x3F)
    b[2] = Trunc[u8](0x80 | c >> 6 & 0x3F)
    b[3] = Trunc[u8](0x80 | c & 0x3F)
    return (b: b, n: 4)
}
