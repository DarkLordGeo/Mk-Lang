// verdict: ok
// case: H56
Two(m: mut Map[&int, &int], k: &&int) {
    a := m.Remove(k)
    b := m.Remove(k)
    Use(&a)
    Use(&b)
}
