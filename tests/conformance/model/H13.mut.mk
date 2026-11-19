// verdict: reject E0605
// case: H13.mut
// note: H13 through `Mut` instead of `Store`
var Latest: Mutex[?&i32] = Mutex.New(none)

Stash(io: Io): Canceled!void {
    n: i32 = 1
    var g := try Latest.Lock(io)
    defer g.Unlock()
    r := g.Mut()
    *r = some(&n) // <- E0605
}
