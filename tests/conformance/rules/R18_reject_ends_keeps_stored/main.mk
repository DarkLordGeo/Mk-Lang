// verdict: reject E0505
// rule: R18
// rule: D-232
// note: after a call to `Clear(mut self, x: &i32 into self) ends self`, `o` still holds what the call stored into it, so `x` ending while `o` is read later is E0505 at `&x`, as without `ends`
import holds

Keep(c: bool): int {
    var o := holds.Empty()
    if c {
        x: i32 = 1
        o.Clear(&x) // <- E0505
    }
    return o.Read()
}
