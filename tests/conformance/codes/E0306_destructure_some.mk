// verdict: reject E0306
// note: `some(v)` on the left of `:=`; an optional is taken apart with `if v := o` or `match`
Get(o: ?int): int {
    some(v) := o // <- E0306
    return v
}
