// verdict: reject E0605
// case: R16.Stash
// also: H13
var Latest: Mutex[?&i32] = Mutex.New(none)

Stash(io: Io): Canceled!void {
    n: i32 = 1
    var g := try Latest.Lock(io)
    defer g.Unlock()
    g.Store(some(&n)) // <- E0605
}
