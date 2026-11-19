// verdict: reject E1206
// note: a C-style for gets one E1206 at the first ; (GR188); the head starts like a BindCond (Name :=), so an E0101 on this line instead means the C-style rule ran too late
Sum(n: int): int {
    var s := 0
    for i := 0; i < n; i += 1 { // <- E1206
        s += i
    }
    return s
}
