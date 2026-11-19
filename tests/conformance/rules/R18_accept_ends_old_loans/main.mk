// verdict: ok
// rule: R18
// rule: D-232
// note: the twin of R18_reject_ends_keeps_stored: `Clear` ends what `o` held before the call (the loan on `y` from `Hold`), so `y` may end while `o` lives on holding only `&z`; with `Hold(&z)` in place of `Clear(&z)` the same body is E0505 at `&y`
import holds

Swap(c: bool): int {
    z: i32 = 2
    var o := holds.Empty()
    if c {
        y: i32 = 1
        o.Hold(&y)
        o.Clear(&z)
    }
    return o.Read()
}
