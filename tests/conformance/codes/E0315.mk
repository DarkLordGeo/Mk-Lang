// verdict: reject E0315
// note: a range pattern is half-open, lo <= x < hi
Digit(c: char): int {
    return match c {
        '9'..'0' { 1 } // <- E0315
        _ { 0 }
    }
}
