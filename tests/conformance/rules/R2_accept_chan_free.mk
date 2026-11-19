// verdict: ok
// rule: R2
// rule: D-241
// note: a channel of a linear `Tok` is linear and is consumed with the allocator it was made with. `Tok` is not `Free`, so `Chan.Free` is E0701 and an empty channel is consumed by `Chan.FreeEmpty(self, a: &Alloc)`
linear struct Tok {
    n: int
}

Drain(h: &Heap): OutOfMemory!void {
    a := h.Alloc()
    tc := try Chan[Tok].New(a, 0)
    tc.Close()
    tc.FreeEmpty(a)
}
