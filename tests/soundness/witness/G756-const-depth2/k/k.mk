export struct Box {
    export m: &Mutex[?&i32]
}

export struct Box2 {
    export b: &Box
}

MakeM(a: &Arena): &Mutex[?&i32] from a {
    m := a.Put(Mutex[?&i32].New(none)) else {
        return MakeM(a)
    }
    return &*m
}

const GB: Box = Box(m: MakeM(EvalArena())) // <- E1002

export const GB2: Box2 = Box2(b: &GB) // <- E1002

export Read(io: Io): i32 {
    var g := GB2.b.m.Lock(io) else {
        return -1
    }
    var v: i32 = -1
    if r := *g.Ref() {
        v = *r
    }
    g.Unlock()
    return v
}
