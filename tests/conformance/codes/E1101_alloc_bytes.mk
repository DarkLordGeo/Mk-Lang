// verdict: reject E1101
// note: Alloc.Bytes and Alloc.FreeBytes hand out untyped memory, so they are unsafe only
Raw(h: &Heap): *u8 {
    return h.Alloc().Bytes(16, 8) // <- E1101
}
