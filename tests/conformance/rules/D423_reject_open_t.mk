// verdict: reject E0701
// rule: D-423
// note: an open T with no Plain or Free bound may be linear
FreeAny[T](a: &Alloc, xs: HeapList[T]) {
    xs.Free(a) // <- E0701
}
