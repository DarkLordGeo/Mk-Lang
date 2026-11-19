// Source: a `Chunks` length below 1, through Slice.Chunks and,
// with an argument, Slice.ChunksMut. The length is 0, read from the argument count (one or
// two) so no constant folds it.
// Site: std's raw.Fail call in Chunks or ChunksMut, code 8, so the message alone;
// the .err moves when std/core/mem_views.mk does.
export Main(sys: &System): int {
    var xs := [1, 2, 3, 4]
    n := sys.args.Len() / 4
    var total := 0
    if sys.args.Len() > 1 {
        for c in xs.ChunksMut(n) {
            c[0] = 0
            total += c.Len()
        }
    } else {
        for c in xs.Chunks(n) {
            total += c.Len()
        }
    }
    return total
}
