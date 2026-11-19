// verdict: reject E0909
// note: anchored at the least-key const of the cycle
const A: int = B + 1 // <- E0909
const B: int = A + 1
