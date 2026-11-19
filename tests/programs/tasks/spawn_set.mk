// The spawn thunk passes the Io by pointer: a task sets a mut argument; a task
// taking (io: Io, a: int, b: int) sees a and b and can use its Io; a NoCancel Io's scope passes
// a working Io to its task. The thunk receives the Io by address in the first argument register.
Set(x: mut int) {
    *x = 42
}

Pair(io: Io, a: int, b: int, out: mut int) {
    io.Yield() else {
        *out = -1
        return
    }
    *out = a * 10 + b
}

export Main(sys: &System): WriteError!void {
    var x := 0
    var ab := 0
    var nc := 0
    scope s := sys.io.Scope() {
        s.go Set(mut x)
        s.go Pair(sys.io, 3, 4, mut ab)
    }
    scope t := sys.io.NoCancel().Scope() {
        t.go Pair(sys.io.NoCancel(), 5, 6, mut nc)
    }
    var out := sys.Stdout()
    try Print(mut out, "x {x} ab {ab} nc {nc}\n", (x, ab, nc))
}
