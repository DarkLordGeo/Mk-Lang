// verdict: ok
// rule: D-340
// note: in a bracket argument `fn(` begins a fn type, so a list of fn values names its element type there
Twice(x: i64): i64 {
    return x * 2
}

Inc(x: i64): i64 {
    return x + 1
}

Run(a: &Arena): OutOfMemory!i64 {
    var fs := List[fn(i64): i64].New(a)
    try fs.Push(Twice)
    try fs.Push(Inc)
    var x: i64 = 3
    for f in fs {
        x = f(x)
    }
    return x
}
