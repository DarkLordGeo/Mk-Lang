// verdict: reject E0503
// case: H59
struct Names {
    all: HeapList[str]
}

Names.First(&self): ?&str {
    return self.all.Get(0)
}

Caller(a: &Alloc) {
    n := Names(all: HeapList[str].New(a))
    r := n.First()
    Names(all) := n // <- E0503
    all.Free(a)
    Use(&r)
}
