// deque, Mk, std.coll.Deque: the workload of ../README.md. A queue held at
// depth 1,000: each of n steps pushes splitmix64(seed)'s next value at the
// back and, past depth 1,000, pops the front; every 16th step instead pushes
// at the front and pops the back. The checksum folds the popped values.
// Usage: deque <n> <seed>.

import std.coll

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    var d := coll.Deque[u64].New(sys.arena)
    var s := seed
    var sum: u64 = 0
    for i in 0..n {
        s = s +% 0x9E3779B97F4A7C15
        x := Mix(s)
        if i & 15 == 15 {
            try d.PushFront(x)
            if d.Len() > 1000 {
                p := d.Pop() else { 0 }
                sum = sum *% 31 +% p
            }
        } else {
            try d.Push(x)
            if d.Len() > 1000 {
                p := d.PopFront() else { 0 }
                sum = sum *% 31 +% p
            }
        }
    }
    l := d.Len()
    var out := sys.Stdout()
    try Print(mut out, "{sum} {l}\n", (sum, l))
}
