// verdict: reject E0605
// note: the const value rule reached without interior mutability: a slice const GS whose element points at an EvalArena view W[i32], judged on the value, is global memory that takes stores, because W's method Keep declares a store into self (`x: &T into self`), so a store of a frame's loan through it is E0605. The pointee holds no Cell, Mutex, atomic or raw pointer, so the rule that refuses such a const passes it, and a view is not in that rule. Keep's body stores nothing (a store into the const's read-only data would fault), so no dead read is shown; the refusal keeps the rule from resting on Keep's body. With the value walk cut (mutant M-D756-constvalue) it is accepted
import k

Stash(): int {
    var n: i32 = 5
    b := k.GS[0]
    b.w.Keep(&n) // <- E0605
    return 0
}

export Main(sys: &System): WriteError!void {
    try sys.Print("{n}\n", (n: Stash()))
}
