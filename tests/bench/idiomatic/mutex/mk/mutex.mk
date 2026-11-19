// mutex, Mk, idiomatic std: the task of ../README.md with std's Mutex[int]:
// t tasks of one scope each lock it n times (Mutex.Lock, a Guard), add 1
// through Guard.Mut and Unlock. Prints the count (t * n), taken back with
// Mutex.Into. Usage: mutex <n> <t>.

Worker(m: &Mutex[int], io: Io, n: int) {
    for _ in 0..n {
        var g := m.Lock(io) else {
            return
        }
        *g.Mut() += 1
        g.Unlock()
    }
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    m := Mutex[int].New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..t {
            sc.go Worker(&m, sys.io, n)
        }
    }
    count := m.Into()
    try sys.Print("{count}\n", (count))
}
