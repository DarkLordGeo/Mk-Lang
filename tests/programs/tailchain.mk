// `become` and range patterns, as a program sees them. The rule and code units are in
// tests/conformance (codes/E0312 to E0315).
// - A chain of 10,000,000 tail calls through a direct call, and one through a table of `fn`
//   values, each runs in the 16 MiB of Main's stack only because every `become` replaces the
//   frame: one function's calls halfway down the chain and at its end see the same
//   StackRemaining. The numbers themselves are not printed (they differ by profile and target),
//   only whether they are equal.
// - A `match` over all 256 values of u8 by ranges and literals, with no `_` arm (exhaustive
//   over the type's whole range), and one over char by half-open ranges.
// - Release turns a plain call in tail position into a jump by itself (LLVM's sibling-call
//   optimisation), so a chain of plain calls would pass there too. Each chain therefore lends a
//   local of its frame to a function reached through a `fn` table (Sinks) before its tail call:
//   the optimiser cannot prove the local does not escape, so it keeps a plain call a call, and a
//   chain of plain calls ends in a stack overflow in release as in dev (checked with this file's
//   `become` replaced by `return`), while `become` replaces the frame in both.
// Expected output: tailchain.out, the same in dev and release.
import std.atomic
import std.runtime

const N: int = 10_000_000

// The StackRemaining the chains' halfway and last calls saw: [0] and [1] the direct chain's, [2]
// and [3] the table chain's.
var Seen: [4]atomic.Atomic[u64] = [atomic.Atomic[u64].New(0), atomic.Atomic[u64].New(0), atomic.Atomic[u64].New(0), atomic.Atomic[u64].New(0)]

// Mark records StackRemaining at a call halfway down a chain (n == first) and at its last call
// (n == 0), both in one function, since frames of two functions may differ in size. Halfway, not
// the first call: release may inline a chain's first steps into the caller, whose frame differs.
Mark(n: int, first: int, at: int) {
    if n == first {
        Seen[at].Store(Trunc[u64](runtime.StackRemaining()), atomic.SeqCst)
    } else if n == 0 {
        Seen[at + 1].Store(Trunc[u64](runtime.StackRemaining()), atomic.SeqCst)
    }
}

// SinkA and SinkB read a frame's local through a reference. Each chain calls one through Sinks, so
// the local's address leaves the frame as far as the optimiser can tell.
SinkA(p: &[4]int): int {
    return p[0]
}

SinkB(p: &[4]int): int {
    return p[1]
}

const Sinks: [2]fn(&[4]int): int = [SinkA, SinkB]

// Count adds one per call, n calls deep, each a tail call to itself.
Count(n: int, acc: int): int {
    Mark(n, N / 2, 0)
    if n == 0 {
        return acc
    }
    pad: [4]int = [n, n, n, n]
    k := Sinks[n % 2](&pad) - n
    become Count(n - 1, acc + 1 + k)
}

// Even and Odd each add their own step and tail-call the table's entry for n's parity, so the
// chain alternates by n: from Even at N, the sum is 15,000,001. Odd gets every even n below N, so
// Mark compares its calls at N / 2 and 0.
Even(n: int, acc: int): int {
    if n == 0 {
        return acc
    }
    pad: [4]int = [n, n, n, n]
    k := Sinks[n % 2](&pad) - n
    become Steps[n % 2](n - 1, acc + 2 + k)
}

Odd(n: int, acc: int): int {
    Mark(n, N / 2, 2)
    if n == 0 {
        return acc
    }
    pad: [4]int = [n, n, n, n]
    k := Sinks[n % 2](&pad) - n
    become Steps[n % 2](n - 1, acc + 1 + k)
}

const Steps: [2]fn(int, int): int = [Even, Odd]

// Byte classes a byte by half-open ranges and literals; the arms cover all 256 values, so no `_`
// arm is needed.
Byte(b: u8): int {
    return match b {
        0..32 { 0 }
        32..127 { 1 }
        127 { 2 }
        128..255 { 3 }
        255 { 4 }
    }
}

// Class classes a char by half-open ranges: lower case, upper case, digit, other.
Class(c: char): int {
    return match c {
        'a'..'{' { 0 }
        'A'..'[' { 1 }
        '0'..':' { 2 }
        _ { 3 }
    }
}

// Same says whether a chain's halfway and last calls saw the same StackRemaining.
Same(at: int): bool {
    return Seen[at].Load(atomic.SeqCst) == Seen[at + 1].Load(atomic.SeqCst) && Seen[at].Load(atomic.SeqCst) != 0
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Count(N, 0)
    try Print(mut out, "become, direct: {r} calls; the stack at the last call is as halfway down: {s}\n", (r: r, s: Same(0)))
    t := Steps[0](N, 0)
    try Print(mut out, "become, through a table of fn values: sum {t}; the stack at the last call is as halfway down: {s}\n", (t: t, s: Same(2)))
    var b: [5]int = [0; 5]
    for i in 0..256 {
        b[Byte(Trunc[u8](i))] += 1
    }
    try Print(mut out, "u8 by ranges: control {a}, printable {p}, delete {d}, high {h}, 255 {e}\n", (a: b[0], p: b[1], d: b[2], h: b[3], e: b[4]))
    var c: [4]int = [0; 4]
    for x in "Range patterns, 1.3: Mk".Scalars() {
        c[Class(x)] += 1
    }
    try Print(mut out, "char by ranges: lower {l}, upper {u}, digit {g}, other {o}\n", (l: c[0], u: c[1], g: c[2], o: c[3]))
}
