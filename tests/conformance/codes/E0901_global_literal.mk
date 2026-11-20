// verdict: reject E0901
// note: a global var's initializer is evaluated at compile time, so an overflow over literals there is the evaluation's E0901, and the decided side of && is never evaluated
var Small: u8 = 200 + 100 // <- E0901

var Never: bool = false && 1 / 0 > 0
