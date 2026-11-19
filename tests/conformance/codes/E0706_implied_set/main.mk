// verdict: reject E0706
// note: the witnesses of an implied set at an imported callee: each instance an implied const set or a skipped combination leaves out is E0706 where it is written, at a call and at a fn value alike, a far value whose arithmetic would overflow included, so no program reaches a body whose callee clause was judged only over the set
import lib

Calls(): int {
    a := lib.Half[16, 5]() // <- E0706
    b := lib.Half[16, -9223372036854775800]() // <- E0706
    c := lib.Half[16, 9223372036854775807]() // <- E0706
    d := lib.Pair[4, 4]() // <- E0706
    return a + b + c + d + lib.Half[32, 16]() + lib.Pair[8, 4]()
}

Values(): int {
    f: fn(): int = lib.Half[16, 5] // <- E0706
    g: fn(): int = lib.Half[16, -9223372036854775800] // <- E0706
    h: fn(): int = lib.Pair[8, 2] // <- E0706
    return f() + g() + h()
}
