// verdict: reject E0204
// needs: types
// note: a private field read from another module, reported by the checker in the types group
import util

Peek(c: &util.Counter): int {
    return c.count // <- E0204
}
