// Why: when the DFA is over its budget, or a call needs a group's
// position, matching must stay linear in the input with no allocation.
//
// What: PikeRun, Thompson's simulation with Pike's per-thread captures, and
// its Queue, a sparse set of pcs.
//
// How: the method of Cox 2009 ("Regular Expression Matching: the Virtual
// Machine Approach") and Go's regexp exec.go, over the byte program of
// compile.mk. Threads sit in sparse sets (Briggs and Torczon 1993) in
// priority order; a match cuts every lower-priority thread, which is
// leftmost-first. A new thread starts at every scalar boundary until a
// match is found, after the older threads, as Go starts one per rune. The
// run tracks the two slots of one group k (for k = 0, the start), so a
// thread is a pc and two ints. Time O(m n), m instructions and n bytes; the
// queues are stack arrays of a size class chosen by m (64, 256, 1,024 or
// 4,096 entries), which every call clears, since std has no uninitialised
// array: 58 bytes an entry when slots are tracked (3.6 KiB at 64, 232 KiB
// at 4,096), 6 bytes when not (IsMatch). Group, whose matches are short,
// runs backtrack.mk first, which clears in proportion to m (L + 1).

// Queue is a sparse set of pcs in insertion (priority) order, with the
// tracked slots of each when M > 1 (with M = 1 nothing is tracked).
struct Queue[const N: int, const M: int] {
    sparse: [N]u16
    pcs: [N]u16
    c0: [M]int
    c1: [M]int
    n: int
}

// Pend is Add's stack of deferred second branches.
struct Pend[const N: int, const M: int] {
    pcs: [N]u16
    c0: [M]int
    c1: [M]int
    n: int
}

// Hit is a match lo .. hi and the tracked group's slots (-1 when unset).
struct Hit {
    lo: int
    hi: int
    g0: int
    g1: int
}

struct Run {
    matched: bool
    best: Hit
}

// Pike finds the leftmost-first match of re in s at or after start (at
// start only when anchored), tracking group k; earliest stops at the
// first match found, which is all IsMatch needs (and then no slot is
// tracked). Assertions see all of s, so ^ and \b at start look before it.
Pike(re: &Regex, s: []u8, start: int, anchored: bool, k: int, earliest: bool): ?Hit {
    n := re.insts.Len()
    if earliest {
        if n <= 64 {
            return PikeRun[64, 1](re, s, start, anchored, k, true)
        }
        if n <= 256 {
            return PikeRun[256, 1](re, s, start, anchored, k, true)
        }
        if n <= 1024 {
            return PikeRun[1024, 1](re, s, start, anchored, k, true)
        }
        return PikeRun[4096, 1](re, s, start, anchored, k, true)
    }
    if n <= 64 {
        return PikeRun[64, 64](re, s, start, anchored, k, false)
    }
    if n <= 256 {
        return PikeRun[256, 256](re, s, start, anchored, k, false)
    }
    if n <= 1024 {
        return PikeRun[1024, 1024](re, s, start, anchored, k, false)
    }
    return PikeRun[4096, 4096](re, s, start, anchored, k, false)
}

PikeRun[const N: int, const M: int](re: &Regex, s: []u8, start: int, anchored: bool, k: int, earliest: bool): ?Hit {
    var a := Queue[N, M](sparse: [0; N], pcs: [0; N], c0: [0; M], c1: [0; M], n: 0)
    var b := Queue[N, M](sparse: [0; N], pcs: [0; N], c0: [0; M], c1: [0; M], n: 0)
    var st := Pend[N, M](pcs: [0; N], c0: [0; M], c1: [0; M], n: 0)
    var run := Run(matched: false, best: Hit(lo: -1, hi: -1, g0: -1, g1: -1))
    var pos := start
    var even := true
    for {
        done := if even {
            Round(re, s, mut a, mut b, mut st, mut run, pos, start, anchored, k, earliest)
        } else {
            Round(re, s, mut b, mut a, mut st, mut run, pos, start, anchored, k, earliest)
        }
        if done || pos >= s.Len() {
            break
        }
        pos += 1
        even = !even
    }
    if run.matched {
        return some(run.best)
    }
    return none
}

// Round runs the threads of cur on the byte at pos into nxt; true when the
// run is over.
Round[const N: int, const M: int](re: &Regex, s: []u8, cur: mut Queue[N, M], nxt: mut Queue[N, M], st: mut Pend[N, M], run: mut Run, pos: int, start: int, anchored: bool, k: int, earliest: bool): bool {
    if cur.n == 0 {
        if run.matched || anchored && pos > start || re.anchor && pos > 0 {
            return true
        }
    }
    if !run.matched && (!anchored || pos == start) && (pos == s.Len() || s[pos] & 0xC0 != 0x80) {
        c0 := if k == 0 { pos } else { -1 }
        Add[N, M](re, s, cur, st, re.start, pos, c0, -1, k)
    }
    insts := re.insts
    edges := re.edges
    var j := 0
    for j < cur.n {
        pc := Cast[int](cur.pcs[j])
        ins := insts[pc]
        var c0 := -1
        var c1 := -1
        if M > 1 {
            c0 = cur.c0[j]
            c1 = cur.c1[j]
        }
        j += 1
        if ins.op == OpMatch {
            run.matched = true
            lo := if k == 0 { c0 } else { start }
            run.best = Hit(lo: lo, hi: pos, g0: c0, g1: c1)
            if earliest {
                cur.n = 0
                return true
            }
            break
        }
        if pos >= s.Len() {
            continue
        }
        c := s[pos]
        if ins.op == OpByte {
            if c >= ins.lo && c <= ins.hi {
                Add[N, M](re, s, nxt, st, ins.x, pos + 1, c0, c1, k)
            }
        } else if ins.op == OpSparse {
            f := Cast[int](ins.x)
            for e in f..f + Cast[int](ins.y) {
                ed := edges[e]
                if c < ed.lo {
                    break
                }
                if c <= ed.hi {
                    Add[N, M](re, s, nxt, st, ed.next, pos + 1, c0, c1, k)
                    break
                }
            }
        }
    }
    cur.n = 0
    return false
}

// Add puts pc and the epsilon closure of it into q, in priority order,
// at pos: splits in order, saves updating the tracked slots of group k,
// assertions tested against s at pos.
Add[const N: int, const M: int](re: &Regex, s: []u8, q: mut Queue[N, M], st: mut Pend[N, M], pc0: u32, pos: int, c00: int, c10: int, k: int) {
    insts := re.insts
    st.n = 0
    Defer[N, M](st, pc0, c00, c10)
    for st.n > 0 {
        st.n -= 1
        var pc := Cast[int](st.pcs[st.n])
        var c0 := c00
        var c1 := c10
        if M > 1 {
            c0 = st.c0[st.n]
            c1 = st.c1[st.n]
        }
        for {
            i := Cast[int](q.sparse[pc])
            if i < q.n && Cast[int](q.pcs[i]) == pc {
                break
            }
            q.sparse[pc] = Trunc[u16](q.n)
            q.pcs[q.n] = Trunc[u16](pc)
            if M > 1 {
                q.c0[q.n] = c0
                q.c1[q.n] = c1
            }
            q.n += 1
            ins := insts[pc]
            op := ins.op
            if op == OpSplit {
                Defer[N, M](st, ins.y, c0, c1)
                pc = Cast[int](ins.x)
                continue
            }
            if op == OpSave {
                if k > 0 {
                    slot := Cast[int](ins.y)
                    if slot == 2 * k {
                        c0 = pos
                    } else if slot == 2 * k + 1 {
                        c1 = pos
                    }
                }
                pc = Cast[int](ins.x)
                continue
            }
            if op == OpNop {
                pc = Cast[int](ins.x)
                continue
            }
            if op == OpAssert && Holds(ins.lo, s, pos) {
                pc = Cast[int](ins.x)
                continue
            }
            break
        }
    }
}

Defer[const N: int, const M: int](st: mut Pend[N, M], pc: u32, c0: int, c1: int) {
    st.pcs[st.n] = Trunc[u16](pc)
    if M > 1 {
        st.c0[st.n] = c0
        st.c1[st.n] = c1
    }
    st.n += 1
}

// Holds tests assertion kind at pos of s. \b and \B are ASCII, as Go's.
Holds(kind: u8, s: []u8, pos: int): bool {
    n := s.Len()
    if kind == ABeginText {
        return pos == 0
    }
    if kind == AEndText {
        return pos == n
    }
    if kind == ABeginLine {
        return pos == 0 || s[pos - 1] == b'\n'
    }
    if kind == AEndLine {
        return pos == n || s[pos] == b'\n'
    }
    before := pos > 0 && Word(s[pos - 1])
    after := pos < n && Word(s[pos])
    if kind == AWord {
        return before != after
    }
    return before == after
}
