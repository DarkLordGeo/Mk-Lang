// Why: Group needs the capture of a short match it already found, and the
// Pike VM's cost grows with the program; a bounded backtracker gets it in
// time proportional to the match.
//
// What: BackRun, RE2's BitState and Go's regexp backtrack.go over the byte
// program of compile.mk, for Group on a known match lo .. hi, with the job
// kinds (BtRun, BtAlt, BtSet0, BtSet1) and the Visit and Back helpers.
//
// How: it explores the threads depth first in priority order and keeps one
// visited bit per (pc, position), so the first match it reaches is the
// leftmost-first one and no (pc, position) is explored twice: time
// O(m (L + 1)) for m instructions and a match of L bytes. The bitset and
// the job stack are stack arrays of a size class chosen by m (L + 1), so a
// call on a short match clears a few hundred bytes whatever the program's
// size. It reads no byte at or past hi: a thread that would take the byte
// at hi gives up the run, and so does a full job stack, and the caller then
// runs the Pike VM, so the answer is the Pike VM's whenever the
// backtracker gives one.

// Job kinds: explore a thread at (pc, pos); the second branch of the
// Split at pc, at pos, still to try (Go's arg = true); restore the
// tracked group's start or end slot to an earlier value.
const BtRun: u64 = 0
const BtAlt: u64 = 1
const BtSet0: u64 = 2
const BtSet1: u64 = 3

// The run's outcome: a match found, none, or given up (the Pike VM
// decides).
const BtFound: u8 = 0
const BtNone: u8 = 1
const BtGiveUp: u8 = 2

// MaxBtBits bounds m (L + 1), the visited bits: Go's maxBacktrackVector
// (256 K bits, 32 KiB).
const MaxBtBits: int = 262144

struct BtOut {
    state: u8
    hi: int
    g0: int
    g1: int
}

// Back runs the backtracker for group k on the match lo .. hi of s, in
// the size class of m (L + 1) bits; past MaxBtBits it gives up at once.
Back(re: &Regex, s: []u8, lo: int, hi: int, k: int): BtOut {
    bits := re.insts.Len() * (hi - lo + 1)
    if bits <= 1024 {
        return BackRun[16, 64](re, s, lo, hi, k)
    }
    if bits <= 8192 {
        return BackRun[128, 256](re, s, lo, hi, k)
    }
    if bits <= 65536 {
        return BackRun[1024, 1024](re, s, lo, hi, k)
    }
    if bits <= MaxBtBits {
        return BackRun[4096, 4096](re, s, lo, hi, k)
    }
    return BtOut(state: BtGiveUp, hi: -1, g0: -1, g1: -1)
}

// BackRun is Back with W words of visited bits and room for J jobs. A
// job is kind | pc << 2 | v << 18, v the position's offset from lo, or
// for a restore the slot's value plus 1 minus lo (0 for unset).
BackRun[const W: int, const J: int](re: &Regex, s: []u8, lo: int, hi: int, k: int): BtOut {
    var vis: [W]u64 = [0; W]
    var jobs: [J]u64 = [0; J]
    width := hi - lo + 1
    insts := re.insts
    edges := re.edges
    give := BtOut(state: BtGiveUp, hi: -1, g0: -1, g1: -1)
    var c0 := -1
    var c1 := -1
    _ = Visit[W](mut vis, Cast[int](re.start) * width)
    jobs[0] = BtRun | Cast[u64](re.start) << 2
    var nj := 1
    for nj > 0 {
        nj -= 1
        j := jobs[nj]
        kind := j & 3
        v := Cast[int](j >> 18)
        if kind == BtSet0 {
            c0 = Unslot(v, lo)
            continue
        }
        if kind == BtSet1 {
            c1 = Unslot(v, lo)
            continue
        }
        var pc := Cast[int](j >> 2 & 0xFFFF)
        var pos := lo + v
        if kind == BtAlt {
            pc = Cast[int](insts[pc].y)
            if !Visit[W](mut vis, pc * width + v) {
                continue
            }
        }
        for {
            ins := insts[pc]
            op := ins.op
            if op == OpMatch {
                return BtOut(state: BtFound, hi: pos, g0: c0, g1: c1)
            }
            if op == OpByte || op == OpSparse {
                if pos >= s.Len() {
                    break
                }
                to := Take(ins, edges, s[pos])
                if to == NoHole {
                    break
                }
                if pos == hi {
                    return give
                }
                pc = Cast[int](to)
                pos += 1
            } else if op == OpSplit {
                if nj == J {
                    return give
                }
                jobs[nj] = BtAlt | Cast[u64](pc) << 2 | Cast[u64](pos - lo) << 18
                nj += 1
                pc = Cast[int](ins.x)
            } else if op == OpSave {
                slot := Cast[int](ins.y)
                if slot == 2 * k || slot == 2 * k + 1 {
                    if nj == J {
                        return give
                    }
                    if slot == 2 * k {
                        jobs[nj] = BtSet0 | Cast[u64](Slot(c0, lo)) << 18
                        c0 = pos
                    } else {
                        jobs[nj] = BtSet1 | Cast[u64](Slot(c1, lo)) << 18
                        c1 = pos
                    }
                    nj += 1
                }
                pc = Cast[int](ins.x)
            } else if op == OpNop {
                pc = Cast[int](ins.x)
            } else if op == OpAssert && Holds(ins.lo, s, pos) {
                pc = Cast[int](ins.x)
            } else {
                break
            }
            if !Visit[W](mut vis, pc * width + pos - lo) {
                break
            }
        }
    }
    return BtOut(state: BtNone, hi: -1, g0: -1, g1: -1)
}

// Visit marks bit b; true when it was clear.
Visit[const W: int](vis: mut [W]u64, b: int): bool {
    w := b >> 6
    m := Cast[u64](1) << Trunc[u64](b & 63)
    if vis[w] & m != 0 {
        return false
    }
    vis[w] = vis[w] | m
    return true
}

// Slot is a slot value as a job stores it: 0 for unset, else p - lo + 1.
Slot(p: int, lo: int): int {
    if p < 0 {
        return 0
    }
    return p - lo + 1
}

Unslot(v: int, lo: int): int {
    if v == 0 {
        return -1
    }
    return v - 1 + lo
}

// Take is where byte instruction ins goes on byte c, or NoHole.
Take(ins: Inst, edges: []Edge, c: u8): u32 {
    if ins.op == OpByte {
        if c >= ins.lo && c <= ins.hi {
            return ins.x
        }
        return NoHole
    }
    f := Cast[int](ins.x)
    for e in f..f + Cast[int](ins.y) {
        ed := edges[e]
        if c < ed.lo {
            return NoHole
        }
        if c <= ed.hi {
            return ed.next
        }
    }
    return NoHole
}
