// verdict: ok
// note: precision at Io: a source-less GetIo(io: Io): &Mutex[?&i32] returns &GM. Io holds its table's raw pointer inline, but it does not count as R19's raw parameter (holdsRaw): the signature names no source and no raw pointer as the user writes it, so the boundary does not refuse the static with E0602. A class P witness of mutant M-D756-io
var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

GetIo(io: Io): &Mutex[?&i32] {
    _ = io
    return &GM
}

export Main(sys: &System): WriteError!void {
    m := GetIo(sys.io)
    var g := m.Lock(sys.io) else {
        return
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    try sys.Print("v {v}\n", (v: v))
}
