// verdict: reject E1101
// rule: EV529
// rule: R19
// note: a call of a bodiless `extern` needs `unsafe`
extern Tick(n: i32): i32

Next(n: i32): i32 {
    return Tick(n) // <- E1101
}
