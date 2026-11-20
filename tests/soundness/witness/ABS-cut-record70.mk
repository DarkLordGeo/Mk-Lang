// verdict: reject E0604
// note: a parameter &W whose p#2 walk meets 69 records ?&(x: B[[i]u8], k: int) and then last: ?&(x: B[[70]u8], c: Cell[?&Mutex[?&i32]]). The walk stops at ANY past 64 records holding distinct instances of B, before last; the stopped referents still take stores, so the store of &GM into r.c is E0604. If accepted, a run reads a dead frame
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

struct B[T] {
    v: T
}

struct W {
    r1: ?&(x: B[[1]u8], k: int)
    r2: ?&(x: B[[2]u8], k: int)
    r3: ?&(x: B[[3]u8], k: int)
    r4: ?&(x: B[[4]u8], k: int)
    r5: ?&(x: B[[5]u8], k: int)
    r6: ?&(x: B[[6]u8], k: int)
    r7: ?&(x: B[[7]u8], k: int)
    r8: ?&(x: B[[8]u8], k: int)
    r9: ?&(x: B[[9]u8], k: int)
    r10: ?&(x: B[[10]u8], k: int)
    r11: ?&(x: B[[11]u8], k: int)
    r12: ?&(x: B[[12]u8], k: int)
    r13: ?&(x: B[[13]u8], k: int)
    r14: ?&(x: B[[14]u8], k: int)
    r15: ?&(x: B[[15]u8], k: int)
    r16: ?&(x: B[[16]u8], k: int)
    r17: ?&(x: B[[17]u8], k: int)
    r18: ?&(x: B[[18]u8], k: int)
    r19: ?&(x: B[[19]u8], k: int)
    r20: ?&(x: B[[20]u8], k: int)
    r21: ?&(x: B[[21]u8], k: int)
    r22: ?&(x: B[[22]u8], k: int)
    r23: ?&(x: B[[23]u8], k: int)
    r24: ?&(x: B[[24]u8], k: int)
    r25: ?&(x: B[[25]u8], k: int)
    r26: ?&(x: B[[26]u8], k: int)
    r27: ?&(x: B[[27]u8], k: int)
    r28: ?&(x: B[[28]u8], k: int)
    r29: ?&(x: B[[29]u8], k: int)
    r30: ?&(x: B[[30]u8], k: int)
    r31: ?&(x: B[[31]u8], k: int)
    r32: ?&(x: B[[32]u8], k: int)
    r33: ?&(x: B[[33]u8], k: int)
    r34: ?&(x: B[[34]u8], k: int)
    r35: ?&(x: B[[35]u8], k: int)
    r36: ?&(x: B[[36]u8], k: int)
    r37: ?&(x: B[[37]u8], k: int)
    r38: ?&(x: B[[38]u8], k: int)
    r39: ?&(x: B[[39]u8], k: int)
    r40: ?&(x: B[[40]u8], k: int)
    r41: ?&(x: B[[41]u8], k: int)
    r42: ?&(x: B[[42]u8], k: int)
    r43: ?&(x: B[[43]u8], k: int)
    r44: ?&(x: B[[44]u8], k: int)
    r45: ?&(x: B[[45]u8], k: int)
    r46: ?&(x: B[[46]u8], k: int)
    r47: ?&(x: B[[47]u8], k: int)
    r48: ?&(x: B[[48]u8], k: int)
    r49: ?&(x: B[[49]u8], k: int)
    r50: ?&(x: B[[50]u8], k: int)
    r51: ?&(x: B[[51]u8], k: int)
    r52: ?&(x: B[[52]u8], k: int)
    r53: ?&(x: B[[53]u8], k: int)
    r54: ?&(x: B[[54]u8], k: int)
    r55: ?&(x: B[[55]u8], k: int)
    r56: ?&(x: B[[56]u8], k: int)
    r57: ?&(x: B[[57]u8], k: int)
    r58: ?&(x: B[[58]u8], k: int)
    r59: ?&(x: B[[59]u8], k: int)
    r60: ?&(x: B[[60]u8], k: int)
    r61: ?&(x: B[[61]u8], k: int)
    r62: ?&(x: B[[62]u8], k: int)
    r63: ?&(x: B[[63]u8], k: int)
    r64: ?&(x: B[[64]u8], k: int)
    r65: ?&(x: B[[65]u8], k: int)
    r66: ?&(x: B[[66]u8], k: int)
    r67: ?&(x: B[[67]u8], k: int)
    r68: ?&(x: B[[68]u8], k: int)
    r69: ?&(x: B[[69]u8], k: int)
    last: ?&(x: B[[70]u8], c: Cell[?&Mutex[?&i32]])
}

GetInto(w: &W) {
    if r := w.last {
        r.c.Store(some(&GM)) // <- E0604
    }
}

Stash(io: Io, depth: int): int {
    if depth > 0 {
        return depth - Stash(io, depth - 1)
    }
    var n: i32 = 5
    hold := (x: B[[70]u8](v: [0; 70]), c: Cell[?&Mutex[?&i32]].New(none))
    w := W(r1: none, r2: none, r3: none, r4: none, r5: none, r6: none, r7: none, r8: none, r9: none, r10: none, r11: none, r12: none, r13: none, r14: none, r15: none, r16: none, r17: none, r18: none, r19: none, r20: none, r21: none, r22: none, r23: none, r24: none, r25: none, r26: none, r27: none, r28: none, r29: none, r30: none, r31: none, r32: none, r33: none, r34: none, r35: none, r36: none, r37: none, r38: none, r39: none, r40: none, r41: none, r42: none, r43: none, r44: none, r45: none, r46: none, r47: none, r48: none, r49: none, r50: none, r51: none, r52: none, r53: none, r54: none, r55: none, r56: none, r57: none, r58: none, r59: none, r60: none, r61: none, r62: none, r63: none, r64: none, r65: none, r66: none, r67: none, r68: none, r69: none, last: some(&hold))
    GetInto(&w)
    if m := hold.c.Load() {
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
