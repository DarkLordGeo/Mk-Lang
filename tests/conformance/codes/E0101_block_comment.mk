// verdict: reject E0101
// note: lexical, /* opens no comment in Mk; comments are // only
Answer(): int {
    /* the answer */ // <- E0101
    return 42
}
