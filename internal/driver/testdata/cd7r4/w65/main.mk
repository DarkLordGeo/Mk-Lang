struct B[T] {
    p: &T
}

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
    g65: ?&B[[65]u8]
}

var GW: Wide = Wide(g1: none, g2: none, g3: none, g4: none, g5: none, g6: none, g7: none, g8: none, g9: none, g10: none, g11: none, g12: none, g13: none, g14: none, g15: none, g16: none, g17: none, g18: none, g19: none, g20: none, g21: none, g22: none, g23: none, g24: none, g25: none, g26: none, g27: none, g28: none, g29: none, g30: none, g31: none, g32: none, g33: none, g34: none, g35: none, g36: none, g37: none, g38: none, g39: none, g40: none, g41: none, g42: none, g43: none, g44: none, g45: none, g46: none, g47: none, g48: none, g49: none, g50: none, g51: none, g52: none, g53: none, g54: none, g55: none, g56: none, g57: none, g58: none, g59: none, g60: none, g61: none, g62: none, g63: none, g64: none, g65: none)

Pick(x: &i32): &Wide from x {
    return &GW
}

export Main(sys: &System) {
    n: i32 = 7
    w := Pick(&n)
    if x := w.g1 {
        _ = x
        sys.Print("some\n") else {}
    } else {
        sys.Print("none\n") else {}
    }
}
