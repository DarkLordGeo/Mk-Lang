// verdict: ok
// rule: D-436
// note: the task keeps its error in a mut ?E slot the parent owns, and the parent prints it after the join; the task takes Io, which is Share, not sys
error Odd(n: int)

Check(n: int): Odd!void {
    if n % 2 == 1 {
        fail Odd(n: n)
    }
}

CheckTask(io: Io, n: int, failed: mut ?Odd) {
    _ = io
    Check(n) else e {
        *failed = some(e)
    }
}

export Main(sys: &System): WriteError!void {
    var a: ?Odd = none
    var b: ?Odd = none
    scope s := sys.io.Scope() {
        s.go CheckTask(sys.io, 2, mut a)
        s.go CheckTask(sys.io, 3, mut b)
    }
    for e in [a, b] {
        if x := e {
            try sys.PrintErr("{x}\n", (x))
        }
    }
}
