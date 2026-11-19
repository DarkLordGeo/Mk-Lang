// verdict: reject E0701
// rule: D-291
// note: an extern var's type is plain, else E0701 with bound Plain, as for std.mmio
import k

Run(): int {
    return k.Size()
}
