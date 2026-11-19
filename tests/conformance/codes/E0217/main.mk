// verdict: reject E0217
// note: a name of a test dependency used outside a test block (doc/packages.md)
import h

export Answer(): int {
    return h.Answer() // <- E0217
}

test "answer" {
    Assert(h.Answer() == 42)
}
