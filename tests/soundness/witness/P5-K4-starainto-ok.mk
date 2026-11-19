// verdict: ok
// note: the narrowing of B's keep: a call passing a local arena to a `*a into xs` parameter, with an empty callee; arr is read while sa lives and sa freed after. Sound (result 321). A rejection, E0505 at the call, means the narrowing is gone (mutant M-D791-narrow)
// One clause, `*a into xs`; the callee does
// nothing; the caller passes a local arena, reads arr while sa
// lives and frees sa after. Sound (result 321). Shows what the clause means at the call.
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
    t := arr[0].Len() * 100 + arr[1].Len() * 10 + arr[2].Len()
    sa.Free()
    return t
}

export Main(sys: &System): WriteError!void {
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}
