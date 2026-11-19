// verdict: reject E0605
// prelude: yes
var Last: Mutex[?&i32] = Mutex.New(none)

Keep(io: Io, p: &Point): Canceled!void {
    var g := try Last.Lock(io)
    defer g.Unlock()
    g.Store(some(&p.x)) // <- E0605
}
