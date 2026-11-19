// verdict: reject E0202
// note: not a std. path, so no std gap can make it pending
import nothing.here // <- E0202

One(): int {
    return 1
}
