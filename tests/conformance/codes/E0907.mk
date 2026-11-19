// verdict: reject E0907
// note: anchored at the first declaration in the cycle
struct Node { // <- E0907
    value: int
    next: Node
}
