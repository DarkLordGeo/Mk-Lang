// verdict: reject E0604
// note: the one referent behind `w` that takes stores is `B[Cell[?&Mutex[?&i32]]]`, the 65th instance of `B`, past the walk's stop; the E0604 for the static stored into it names the bound (`bound: more than 64 instances of `B``). The bound changes no verdict: with fewer fields it is the same E0604, without the fact
struct B[T] {
    v: T
}

var GM: Mutex[?&i32] = Mutex.New(none)

struct Wide {
    g1: ?&B[[1]u8]
    g2: ?&B[[2]u8]
    g3: ?&B[[3]u8]
    g4: ?&B[[4]u8]
    g5: ?&B[[5]u8]
    g6: ?&B[[6]u8]
    g7: ?&B[[7]u8]
    g8: ?&B[[8]u8]
    g9: ?&B[[9]u8]
    g10: ?&B[[10]u8]
    g11: ?&B[[11]u8]
    g12: ?&B[[12]u8]
    g13: ?&B[[13]u8]
    g14: ?&B[[14]u8]
    g15: ?&B[[15]u8]
    g16: ?&B[[16]u8]
    g17: ?&B[[17]u8]
    g18: ?&B[[18]u8]
    g19: ?&B[[19]u8]
    g20: ?&B[[20]u8]
    g21: ?&B[[21]u8]
    g22: ?&B[[22]u8]
    g23: ?&B[[23]u8]
    g24: ?&B[[24]u8]
    g25: ?&B[[25]u8]
    g26: ?&B[[26]u8]
    g27: ?&B[[27]u8]
    g28: ?&B[[28]u8]
    g29: ?&B[[29]u8]
    g30: ?&B[[30]u8]
    g31: ?&B[[31]u8]
    g32: ?&B[[32]u8]
    g33: ?&B[[33]u8]
    g34: ?&B[[34]u8]
    g35: ?&B[[35]u8]
    g36: ?&B[[36]u8]
    g37: ?&B[[37]u8]
    g38: ?&B[[38]u8]
    g39: ?&B[[39]u8]
    g40: ?&B[[40]u8]
    g41: ?&B[[41]u8]
    g42: ?&B[[42]u8]
    g43: ?&B[[43]u8]
    g44: ?&B[[44]u8]
    g45: ?&B[[45]u8]
    g46: ?&B[[46]u8]
    g47: ?&B[[47]u8]
    g48: ?&B[[48]u8]
    g49: ?&B[[49]u8]
    g50: ?&B[[50]u8]
    g51: ?&B[[51]u8]
    g52: ?&B[[52]u8]
    g53: ?&B[[53]u8]
    g54: ?&B[[54]u8]
    g55: ?&B[[55]u8]
    g56: ?&B[[56]u8]
    g57: ?&B[[57]u8]
    g58: ?&B[[58]u8]
    g59: ?&B[[59]u8]
    g60: ?&B[[60]u8]
    g61: ?&B[[61]u8]
    g62: ?&B[[62]u8]
    g63: ?&B[[63]u8]
    g64: ?&B[[64]u8]
    last: ?&B[Cell[?&Mutex[?&i32]]]
}

Put(w: &Wide) {
    l := w.last else {
        return
    }
    l.v.Store(some(&GM)) // <- E0604
}
