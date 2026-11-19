// verdict: reject E0306
// rule: D-910
// note: exhaustiveness over intervals: the half-open ranges leave 127 uncovered (32..127 stops before it), so the match misses a case
Class(b: u8): int {
    return match b { // <- E0306
        0..32 { 0 }
        32..127 { 1 }
        128..255 { 3 }
        255 { 4 }
    }
}
