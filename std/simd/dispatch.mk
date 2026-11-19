// Why: code that has a faster form for some CPUs needs to pick it at run
// time without each caller writing the feature test and the unsafe call.
//
// What: Choice, one entry of a dispatch table, and Dispatch, which picks the
// first entry whose features the running CPU has.
//
// How: Dispatch is the safe form of the cpu clause's call rule: the test runs
// once per call, and the caller names each choice's function inside
// `unsafe`, whose reason is that `needs` covers the function's clause.

/// One entry of a dispatch table: the function f, to be used when the
/// running CPU has every feature in needs. Plain.
export struct Choice[F] {
    /// The CPU features f needs, all of which the running CPU must have.
    export needs: CpuFeatures
    /// The function to use when the running CPU has every feature in needs.
    export f: F
}

/// Returns the f of the first choice whose needs the running CPU has, or
/// base when none matches. Order the choices best first. The CPU is read
/// once per program and cached. O(choices.Len()), two word compares per
/// choice; it allocates nothing. At compile time the CPU is the target's
/// baseline, so a choice that needs more is never taken there.
export Dispatch[F: Plain](choices: []Choice[F], base: F): F {
    have := Cpu()
    for i in 0..choices.Len() {
        if have.HasAll(&choices[i].needs) {
            return choices[i].f
        }
    }
    return base
}
