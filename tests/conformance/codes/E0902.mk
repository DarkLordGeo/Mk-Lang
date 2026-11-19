// verdict: reject E0902
// note: the fuel stated at the evaluation is too small, so the evaluation runs out of fuel
Count(n: int): int {
    var s := 0
    for i in 0..n {
        s += i
    }
    return s
}

const Total: int = Count(1_000) fuel 100 // <- E0902
