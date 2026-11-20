// verdict: reject E0902
// note: a named const as a const's fuel is that const's value: 1,000 steps run out where the default would not
Spin(): int {
    var n := 0
    for i in 0..10_000 {
        n += i % 7
    }
    return n
}

const Limit = 1000

const Total = Spin() fuel Limit // <- E0902
