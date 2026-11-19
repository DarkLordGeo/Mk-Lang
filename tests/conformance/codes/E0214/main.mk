// verdict: reject E0214
// note: an import of another package's internal module (doc/packages.md)
import util.internal.tok // <- E0214

export Count(): int {
    return tok.Count()
}
