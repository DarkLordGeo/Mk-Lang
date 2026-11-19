// verdict: ok
// case: H31
Susp(io: Io): Canceled!void {
    var n := 0
    r := mut n
    try io.Yield()
    *r = 1
}
