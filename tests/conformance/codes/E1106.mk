// verdict: reject E1106
// note: a cpu clause names only features from the CPU feature table of doc/reference.md
Count(xs: []u32): int cpu "avx9" { // <- E1106
    return xs.Len()
}
