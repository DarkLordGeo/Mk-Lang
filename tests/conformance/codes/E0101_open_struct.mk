// verdict: reject E0101
// note: `open` is a modifier only before `enum` and `type`
open struct Point { // <- E0101
    x: int
}
