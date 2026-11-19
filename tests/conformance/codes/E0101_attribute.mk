// verdict: reject E0101
// note: lexical, # starts no attribute in Mk
#[inline] // <- E0101
Fast(): int {
    return 1
}
