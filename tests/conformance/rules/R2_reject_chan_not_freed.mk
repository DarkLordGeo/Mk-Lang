// verdict: reject E0402
// rule: R2
// rule: D-241
// note: the twin of R2_accept_chan_free: `Close` does not consume the channel, only `Free` does, so the linear channel is not consumed when its scope ends
linear struct Tok {
    n: int
}

Drain(h: &Heap): OutOfMemory!void {
    a := h.Alloc()
    tc := try Chan[Tok].New(a, 0) // <- E0402
    tc.Close()
}
