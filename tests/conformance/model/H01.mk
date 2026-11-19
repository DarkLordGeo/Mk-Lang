// verdict: ok
// case: H01
Lookup(m: mut Map[int, int], k: int): OutOfMemory!mut int {
    if v := m.GetMut(&k) {
        return v
    }
    try m.Set(k, 0)
    return m.GetMut(&k).Must()
}
