MakeM(a: &Arena): &Mutex[?&i32] from a {
    m := a.Put(Mutex[?&i32].New(none)) else {
        return MakeM(a)
    }
    return &*m
}

export const GO: ?&Mutex[?&i32] = some(MakeM(EvalArena())) // <- E1002

export Read(io: Io): i32 {
    var v: i32 = -1
    if m := GO {
        var g := m.Lock(io) else {
            return -1
        }
        if r := *g.Ref() {
            v = *r
        }
        g.Unlock()
    }
    return v
}
