// verdict: reject E1001
// note: the Send hole through Sub: a Sub of an Arena.NewIn arena over a Counting moves into a task. Judged by std's TestWitnesses
import std.mem

// p2: a Sub of an arena over a Counting moved into a task. Must be rejected.
Work(a: Arena) {
    var b := a
    for _ in 0..10000 {
        _ = b.Dup("racing bytes that will need new chunks soon enough") else {
            break
        }
    }
    b.Free()
}

export Main(sys: &System): OutOfMemory!void {
    buf := try sys.arena.Slice[u8](1048576)
    fixed := mem.Fixed.New(buf)
    counting := mem.Counting.New(fixed.Alloc())
    var arena := try Arena.NewIn(counting.Alloc())
    defer arena.Free()
    scope s := sys.io.Scope() {
        sub := try arena.Sub()
        s.go Work(sub) // <- E1001
        for _ in 0..10000 {
            _ = arena.Dup("parent bytes that need chunks of their own as well") else {
                break
            }
        }
    }
}
