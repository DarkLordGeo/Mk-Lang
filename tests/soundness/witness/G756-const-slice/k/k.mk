export struct Box {
    export m: &Mutex[?&i32]
}

MakeM(a: &Arena): &Mutex[?&i32] from a {
    m := a.Put(Mutex[?&i32].New(none)) else {
        return MakeM(a)
    }
    return &*m
}

export const GS: []Box = [Box(m: MakeM(EvalArena()))] // <- E1002

export Read(io: Io): i32 {
    var g := GS[0].m.Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}
