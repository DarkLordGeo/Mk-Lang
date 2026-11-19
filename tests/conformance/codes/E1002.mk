// verdict: reject E1002
// note: a var global whose type holds a raw pointer is not Share; the checker's global form
struct Raw {
    p: ?*u8
}

var Last: Raw = Raw(p: none) // <- E1002
