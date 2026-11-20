// verdict: reject E0204
// needs: types
// note: a method without export is private to its module: a call through a value, in namespace form, as a value, with explicit arguments or by a loop's Next is E0204; an exported method, an impl's method and the module's own calls of the private one are not
import util

Use(c: &util.Counter): int {
    a := c.Get()
    b := util.Counter.Get(c)
    d := c.Name()
    e := c.Secret()             // <- E0204
    f := util.Counter.Secret(c) // <- E0204
    g := util.Same.Secret(c)    // <- E0204
    return a + b + d + e + f + g
}

Value(): fn(&util.Counter): int {
    return util.Counter.Secret // <- E0204
}

Generic(c: &util.Counter): int {
    return util.Counter.Pick[int](c, 3) // <- E0204
}

Loop(): int {
    var n := 0
    for x in util.Start() { // <- E0204
        n += x
    }
    return n
}
