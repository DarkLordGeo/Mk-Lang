// verdict: reject E0902
// note: the default fuel of 4 million steps runs out on a loop that never ends
Spin(): int {
    var n := 0
    for {
        n +%= 1
    }
}

const Forever: int = Spin() // <- E0902
