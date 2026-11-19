// verdict: reject E0901
// note: a panic (divide by zero) during const evaluation
Div(a: int, b: int): int {
    return a / b
}

const Bad: int = Div(1, 0) // <- E0901
