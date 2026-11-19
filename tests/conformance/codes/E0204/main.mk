// verdict: reject E0204
// needs: names
// note: m.X where X exists in m without export
import util

Get(): int {
    return util.Helper() // <- E0204
}
