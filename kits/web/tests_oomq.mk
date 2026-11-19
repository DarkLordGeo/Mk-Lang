// Why: Query must fail with OutOfMemory, not answer, when its arena is full.
// What: one test of Query on an exhausted arena.
// How: fills a fixed-size arena, then asks for a name whose pair holds an
// escape.

import std.mem

test "Query on an exhausted arena fails rather than answer" (sys: &System) {
    var buf := try sys.arena.Slice[u8](4096 + 64)
    var f := mem.Fixed.New(buf)
    var ar := try Arena.NewIn(f.Alloc())
    defer ar.Free()
    var k := 0
    for {
        _ = ar.Slice[u8](8) else {
            break
        }
        k += 1
    }
    req := TwReq("GET", "/", "na%6De=x")
    got := Query(&ar, &req, "name") else {
        return
    }
    Assert(got.IsNone())
    Assert(false)
}
