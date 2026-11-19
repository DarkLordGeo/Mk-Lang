// verdict: reject E0205
// note: the cycle a -> b -> a; reported at the import in the lexicographically first module of the cycle
import a

Get(): int {
    return a.One()
}
