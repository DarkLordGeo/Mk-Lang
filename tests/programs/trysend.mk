// Chan[T].TrySend: it sends without waiting. It answers none when it sent (room in
// the ring, or a receiver parked in Recv), some(x) with x given back whole when the channel is
// full, and Closed when the channel is closed, with x still the caller's. FIFO order survives a
// refused send. A capacity-0 channel with no parked receiver gives x back. A linear value comes
// back on both refusals and is freed by the caller, so the program ends with nothing leaked.
// Expected output: trysend.out.

// Try sends x without waiting and says what TrySend did with it.
Try(w: mut Writer, c: &Chan[int], x: int): WriteError!void {
    r := c.TrySend(x) else {
        try Print(w, "try {x}: closed, kept {x}\n", (x: x))
        return
    }
    match r {
        some(back) {
            try Print(w, "try {x}: full, given back {back}\n", (x: x, back: back))
        }
        none {
            try Print(w, "try {x}: sent\n", (x: x))
        }
    }
}

// Take receives one value and prints it.
Take(w: mut Writer, c: &Chan[int], io: Io): WriteError!void {
    v := c.Recv(io) else {
        try Print(w, "recv: canceled or closed\n")
        return
    }
    try Print(w, "recv {v}\n", (v: v))
}

// TryList sends a linear list of n values without waiting. When the channel refuses it, the
// caller frees what comes back. The answer is n when it was sent, -k when the channel was full and
// gave back a list of k, and 1000 + k when it was closed and the caller kept a list of k.
TryList(h: &Heap, c: &Chan[HeapList[int]], n: int): int {
    var xs := HeapList[int].New(h.Alloc())
    for i in 0..n {
        xs.Push(h.Alloc(), i) else {}
    }
    back := c.TrySend(xs) else {
        k := xs.Len()
        xs.Free(h.Alloc())
        return 1000 + k
    }
    match back {
        some(ys) {
            k := ys.Len()
            ys.Free(h.Alloc())
            return -k
        }
        none {
            return n
        }
    }
}

// Said prints TryList's answer for a list of n.
Said(w: mut Writer, n: int, r: int): WriteError!void {
    if r >= 1000 {
        try Print(w, "list of {n}: closed, kept {k}\n", (n: n, k: r - 1000))
    } else if r < 0 {
        try Print(w, "list of {n}: full, given back {k}\n", (n: n, k: -r))
    } else {
        try Print(w, "list of {n}: sent\n", (n: n))
    }
}

// Linear runs the linear channel's part and prints nothing, so no early exit of a failed print
// leaves a linear value unconsumed. It answers the three TryList answers and the received length.
Linear(sys: &System): (r3: int, r4: int, got: int, r5: int) {
    a := sys.heap.Alloc()
    l := Chan[HeapList[int]].New(a, 1) else {
        return (r3: 0, r4: 0, got: -1, r5: 0)
    }
    r3 := TryList(sys.heap, &l, 3)
    r4 := TryList(sys.heap, &l, 4)
    var got := -1
    held := l.Recv(sys.io) else {
        l.Close()
        l.FreeEmpty(a)
        return (r3: r3, r4: r4, got: -1, r5: 0)
    }
    got = held.Len()
    held.Free(sys.heap.Alloc())
    l.Close()
    r5 := TryList(sys.heap, &l, 5)
    l.FreeEmpty(a)
    return (r3: r3, r4: r4, got: got, r5: r5)
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    a := sys.heap.Alloc()

    try Print(mut out, "capacity 2\n")
    c := Chan[int].New(a, 2) else {
        try Print(mut out, "no channel\n")
        return
    }
    try Try(mut out, &c, 1)
    try Try(mut out, &c, 2)
    try Try(mut out, &c, 3)
    try Take(mut out, &c, sys.io)
    try Try(mut out, &c, 4)
    try Try(mut out, &c, 5)
    try Take(mut out, &c, sys.io)
    try Take(mut out, &c, sys.io)
    c.Close()
    try Try(mut out, &c, 6)
    c.FreeEmpty(a)

    try Print(mut out, "capacity 0\n")
    z := Chan[int].New(a, 0) else {
        try Print(mut out, "no channel\n")
        return
    }
    try Try(mut out, &z, 7)
    z.Close()
    try Try(mut out, &z, 8)
    z.FreeEmpty(a)

    r := Linear(sys)
    try Print(mut out, "linear values, capacity 1\n")
    try Said(mut out, 3, r.r3)
    try Said(mut out, 4, r.r4)
    try Print(mut out, "recv a list of {k}\n", (k: r.got))
    try Said(mut out, 5, r.r5)
}
