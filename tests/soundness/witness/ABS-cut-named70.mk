// verdict: reject E0604
// note: as ABS-cut-record70 with named referents: 69 fields ?&B[[i]u8] and then last: ?&B[Cell[?&Mutex[?&i32]]]. The walk stops at ANY past 64 instances of B, before last; the stopped referent still takes stores, so the store of &GM into r.v is E0604. If accepted, a run reads a dead frame
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

struct B[T] {
    v: T
}

struct W {
    r1: ?&B[[1]u8]
    r2: ?&B[[2]u8]
    r3: ?&B[[3]u8]
    r4: ?&B[[4]u8]
    r5: ?&B[[5]u8]
    r6: ?&B[[6]u8]
    r7: ?&B[[7]u8]
    r8: ?&B[[8]u8]
    r9: ?&B[[9]u8]
    r10: ?&B[[10]u8]
    r11: ?&B[[11]u8]
    r12: ?&B[[12]u8]
    r13: ?&B[[13]u8]
    r14: ?&B[[14]u8]
    r15: ?&B[[15]u8]
    r16: ?&B[[16]u8]
    r17: ?&B[[17]u8]
    r18: ?&B[[18]u8]
    r19: ?&B[[19]u8]
    r20: ?&B[[20]u8]
    r21: ?&B[[21]u8]
    r22: ?&B[[22]u8]
    r23: ?&B[[23]u8]
    r24: ?&B[[24]u8]
    r25: ?&B[[25]u8]
    r26: ?&B[[26]u8]
    r27: ?&B[[27]u8]
    r28: ?&B[[28]u8]
    r29: ?&B[[29]u8]
    r30: ?&B[[30]u8]
    r31: ?&B[[31]u8]
    r32: ?&B[[32]u8]
    r33: ?&B[[33]u8]
    r34: ?&B[[34]u8]
    r35: ?&B[[35]u8]
    r36: ?&B[[36]u8]
    r37: ?&B[[37]u8]
    r38: ?&B[[38]u8]
    r39: ?&B[[39]u8]
    r40: ?&B[[40]u8]
    r41: ?&B[[41]u8]
    r42: ?&B[[42]u8]
    r43: ?&B[[43]u8]
    r44: ?&B[[44]u8]
    r45: ?&B[[45]u8]
    r46: ?&B[[46]u8]
    r47: ?&B[[47]u8]
    r48: ?&B[[48]u8]
    r49: ?&B[[49]u8]
    r50: ?&B[[50]u8]
    r51: ?&B[[51]u8]
    r52: ?&B[[52]u8]
    r53: ?&B[[53]u8]
    r54: ?&B[[54]u8]
    r55: ?&B[[55]u8]
    r56: ?&B[[56]u8]
    r57: ?&B[[57]u8]
    r58: ?&B[[58]u8]
    r59: ?&B[[59]u8]
    r60: ?&B[[60]u8]
    r61: ?&B[[61]u8]
    r62: ?&B[[62]u8]
    r63: ?&B[[63]u8]
    r64: ?&B[[64]u8]
    r65: ?&B[[65]u8]
    r66: ?&B[[66]u8]
    r67: ?&B[[67]u8]
    r68: ?&B[[68]u8]
    r69: ?&B[[69]u8]
    last: ?&B[Cell[?&Mutex[?&i32]]]
}

GetInto(w: &W) {
    if r := w.last {
        r.v.Store(some(&GM)) // <- E0604
    }
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    hold := B[Cell[?&Mutex[?&i32]]](v: Cell[?&Mutex[?&i32]].New(none))
    w := W(r1: none, r2: none, r3: none, r4: none, r5: none, r6: none, r7: none, r8: none, r9: none, r10: none, r11: none, r12: none, r13: none, r14: none, r15: none, r16: none, r17: none, r18: none, r19: none, r20: none, r21: none, r22: none, r23: none, r24: none, r25: none, r26: none, r27: none, r28: none, r29: none, r30: none, r31: none, r32: none, r33: none, r34: none, r35: none, r36: none, r37: none, r38: none, r39: none, r40: none, r41: none, r42: none, r43: none, r44: none, r45: none, r46: none, r47: none, r48: none, r49: none, r50: none, r51: none, r52: none, r53: none, r54: none, r55: none, r56: none, r57: none, r58: none, r59: none, r60: none, r61: none, r62: none, r63: none, r64: none, r65: none, r66: none, r67: none, r68: none, r69: none, last: some(&hold))
    GetInto(&w)
    if m := hold.v.Load() {
        var g := m.Lock(io) else {
            return -2
        }
        g.Store(some(&n))
        g.Unlock()
    }
    return 0
}

Paint(depth: int, p: &i32): i32 {
    var x: [64]i32 = [*p; 64]
    if depth == 0 {
        return x[3]
    }
    return Paint(depth - 1, &x[1]) +% x[2]
}

Read(io: Io): i32 {
    var g := GM.Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}

export Main(sys: &System): WriteError!void {
    if Stash(sys.io, 3) == -1000 {
        return
    }
    before := Read(sys.io)
    var c: i32 = 1515870810
    _ = Paint(8, &c)
    after := Read(sys.io)
    try sys.Print("before {before} after {after}\n", (before, after))
}
