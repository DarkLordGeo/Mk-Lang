// fannkuch, Mk: the reference algorithm of ../README.md (the Benchmarks
// Game's fannkuch-redux, sequential). Three []int of n from the arena, every
// index bounds checked, every integer + and - checked.
// Usage: fannkuch <n>. Prints checksum * 1000 + maxflips.

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    perm := try sys.arena.Slice[int](n)
    perm1 := try sys.arena.Slice[int](n)
    count := try sys.arena.Slice[int](n)
    for i in 0..n {
        perm1[i] = i
    }
    var r := n
    var perm_count := 0
    var checksum := 0
    var max_flips := 0
    var done := false
    for !done {
        for r != 1 {
            count[r - 1] = r
            r -= 1
        }
        for i in 0..n {
            perm[i] = perm1[i]
        }
        var flips := 0
        var k := perm[0]
        for k != 0 {
            var i := 0
            var j := k
            for i < j {
                t := perm[i]
                perm[i] = perm[j]
                perm[j] = t
                i += 1
                j -= 1
            }
            flips += 1
            k = perm[0]
        }
        if flips > max_flips {
            max_flips = flips
        }
        if perm_count % 2 == 0 {
            checksum += flips
        } else {
            checksum -= flips
        }
        for {
            if r == n {
                done = true
                break
            }
            perm0 := perm1[0]
            for i in 0..r {
                perm1[i] = perm1[i + 1]
            }
            perm1[r] = perm0
            count[r] -= 1
            if count[r] > 0 {
                break
            }
            r += 1
        }
        perm_count += 1
    }
    result := checksum * 1000 + max_flips
    var out := sys.Stdout()
    try Print(mut out, "{result}\n", (result))
}
