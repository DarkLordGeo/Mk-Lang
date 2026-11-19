// Why: Replace's template may name any group, so each match needs every
// capture slot, in one pass and without an allocation per match.
//
// What: Caps, NewCaps, Caps.Run, Caps.Got and Caps.Close: the
// leftmost-first match anchored at one offset with every capture slot.
//
// How: pike.mk's simulation with a slot row per thread. The epsilon closure
// works on one slot vector that a stack of restore frames (Frame) puts
// back, as Rust regex-automata's pikevm.rs does, instead of copying the
// vector into every deferred branch; a row is copied only into a thread
// that reads a byte or matches. Time O(m n w), m instructions, n bytes from
// the start until the threads die, w = 2 * (groups + 1) slots; the scratch
// is made once per Replace in its arena (about 16 m w + 24 m bytes) and
// reused for every match.

// Frame is a closure step: explore pc (slot -1), or put val back in slot.
struct Frame {
    pc: u32
    slot: int
    val: int
}

// Caps is the scratch over m instructions and w slots: two thread lists,
// q = 0 or 1, at offset q * m in sparse and dense and q * m * w in slots
// (a thread's row is its index in its list), their lengths, the closure's
// stack and working vector, and best, the slots of the match found.
struct Caps {
    m: int
    w: int
    sparse: List[u32]
    dense: List[u32]
    slots: List[int]
    n0: int
    n1: int
    stack: List[Frame]
    cur: List[int]
    best: List[int]
}

// NewCaps is the scratch for re in a, empty when need is false.
NewCaps(a: &Arena, re: &Regex, need: bool): OutOfMemory!Caps from a {
    var c := Caps(m: 0, w: 0, sparse: List[u32].New(a), dense: List[u32].New(a), slots: List[int].New(a), n0: 0, n1: 0, stack: List[Frame].New(a), cur: List[int].New(a), best: List[int].New(a))
    if !need {
        return c
    }
    c.m = re.insts.Len()
    c.w = 2 * (re.groups + 1)
    for _ in 0..2 * c.m {
        try c.sparse.Push(0)
        try c.dense.Push(0)
    }
    for _ in 0..2 * c.m * c.w {
        try c.slots.Push(-1)
    }
    for _ in 0..c.w {
        try c.cur.Push(-1)
        try c.best.Push(-1)
    }
    return c
}

// Run finds the leftmost-first match of re in s anchored at lo and puts
// its slots in best (-1 for a group that took no part); false when there
// is none. Assertions see all of s.
Caps.Run(mut self, re: &Regex, s: []u8, lo: int): OutOfMemory!bool {
    insts := re.insts
    edges := re.edges
    m := self.m
    w := self.w
    for k in 0..w {
        self.cur[k] = -1
    }
    self.cur[0] = lo
    self.n0 = 0
    self.n1 = 0
    try self.Close(re, s, 0, re.start, lo)
    var matched := false
    var q := 0
    var pos := lo
    for {
        n := if q == 1 { self.n1 } else { self.n0 }
        if n == 0 {
            break
        }
        nq := 1 - q
        if nq == 0 {
            self.n0 = 0
        } else {
            self.n1 = 0
        }
        for j in 0..n {
            pc := Cast[int](self.dense[q * m + j])
            ins := insts[pc]
            row := (q * m + j) * w
            if ins.op == OpMatch {
                matched = true
                for k in 0..w {
                    self.best[k] = self.slots[row + k]
                }
                self.best[1] = pos
                // Leftmost-first: a match cuts every lower thread.
                break
            }
            if pos >= s.Len() {
                continue
            }
            c := s[pos]
            var next: u32 = NoHole
            if ins.op == OpByte {
                if c >= ins.lo && c <= ins.hi {
                    next = ins.x
                }
            } else if ins.op == OpSparse {
                f := Cast[int](ins.x)
                for e in f..f + Cast[int](ins.y) {
                    ed := edges[e]
                    if c < ed.lo {
                        break
                    }
                    if c <= ed.hi {
                        next = ed.next
                        break
                    }
                }
            }
            if next != NoHole {
                for k in 0..w {
                    self.cur[k] = self.slots[row + k]
                }
                try self.Close(re, s, nq, next, pos + 1)
            }
        }
        if pos >= s.Len() {
            break
        }
        pos += 1
        q = nq
    }
    return matched
}

// Close adds pc0 and its epsilon closure to list q at pos, in priority
// order, with the slots of cur as Save updates them; cur is as it was
// when Close returns.
Caps.Close(mut self, re: &Regex, s: []u8, q: int, pc0: u32, pos: int): OutOfMemory!void {
    insts := re.insts
    m := self.m
    w := self.w
    self.stack.Clear()
    try self.stack.Push(Frame(pc: pc0, slot: -1, val: 0))
    for f := self.stack.Pop() {
        if f.slot >= 0 {
            self.cur[f.slot] = f.val
            continue
        }
        var pc := Cast[int](f.pc)
        for {
            n := if q == 1 { self.n1 } else { self.n0 }
            i := Cast[int](self.sparse[q * m + pc])
            if i < n && Cast[int](self.dense[q * m + i]) == pc {
                break
            }
            self.sparse[q * m + pc] = Trunc[u32](n)
            self.dense[q * m + n] = Trunc[u32](pc)
            if q == 0 {
                self.n0 = n + 1
            } else {
                self.n1 = n + 1
            }
            ins := insts[pc]
            op := ins.op
            if op == OpSplit {
                try self.stack.Push(Frame(pc: ins.y, slot: -1, val: 0))
                pc = Cast[int](ins.x)
                continue
            }
            if op == OpSave {
                slot := Cast[int](ins.y)
                if slot < w {
                    try self.stack.Push(Frame(pc: 0, slot: slot, val: self.cur[slot]))
                    self.cur[slot] = pos
                }
                pc = Cast[int](ins.x)
                continue
            }
            if op == OpNop {
                pc = Cast[int](ins.x)
                continue
            }
            if op == OpAssert {
                if Holds(ins.lo, s, pos) {
                    pc = Cast[int](ins.x)
                    continue
                }
                break
            }
            if op == OpByte || op == OpSparse || op == OpMatch {
                row := (q * m + n) * w
                for k in 0..w {
                    self.slots[row + k] = self.cur[k]
                }
            }
            break
        }
    }
}

// Got is group k's span in best, or none when it took no part.
Caps.Got(&self, k: int): ?Match {
    if k < 0 || 2 * k + 1 >= self.w {
        return none
    }
    lo := self.best[2 * k]
    hi := self.best[2 * k + 1]
    if lo < 0 || hi < 0 {
        return none
    }
    return some(Match(lo: lo, hi: hi))
}
