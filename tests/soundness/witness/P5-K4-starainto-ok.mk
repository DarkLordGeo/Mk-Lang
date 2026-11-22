// verdict: ok
// note: the narrowing of B's keep: a call passing a local arena to a `*a into xs` parameter, with an empty callee; sa is freed and arr read after, since what the call stored into arr is the arena's handle, not its memory. Sound (result 321). A rejection, E0503 at the free, means the narrowing is gone (mutant M-D791-narrow)
// One clause, `*a into xs`; the callee does
// nothing; the caller passes a local arena, frees sa, then
// reads arr. Sound (result 321). Shows what the clause means at the call.
Reverse(xs: mut []str, a: &Arena): int *a into xs {
    return 0
}

Run(sys: &System): int {
    var sa := Arena.New(sys.heap) else {
        return -1
    }
    var arr: [3]str = ["ccc", "bb", "a"]
    r := Reverse(mut arr[..], &sa)
    if r != 0 {
        sa.Free()
        return r
    }
    sa.Free()
    return arr[0].Len() * 100 + arr[1].Len() * 10 + arr[2].Len()
}

export Main(sys: &System): WriteError!void {
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}
