// verdict: reject E1221
// note: `dyn I` names the type `gen dyn.Of(I)` declares
interface Shape {
    Area(&self): int
}

Total(s: dyn Shape): int { // <- E1221
    return 0
}
