// The C side of the codegen kernel intsum (../intsum.c), compiled by clang 21
// into a native object that tools/bench links into this program (README "The
// codegen table"), and the 64-byte alignment of the Mk buffer.

extern bench_c_intsum_fill(seed: u64, n: i64)
extern bench_c_intsum_run(n: i64, passes: i64): i64
extern bench_c_intsum_addr(xs: []i64): u64

export CFill(seed: u64, n: int) {
    unsafe "bench_c_intsum_fill is in intsum.c, linked into the benchmark build; it writes only its own static buffer, n is at most its capacity (checked by Main)" {
        bench_c_intsum_fill(seed, Cast[i64](n))
    }
}

export CRun(n: int, passes: int): i64 {
    unsafe "bench_c_intsum_run is in intsum.c; it reads and writes only its own static buffer" {
        return bench_c_intsum_run(Cast[i64](n), Cast[i64](passes))
    }
}

// Addr is the data address of xs as an integer.
export Addr(xs: []i64): u64 {
    unsafe "bench_c_intsum_addr returns the slice's data address as an integer and touches no memory" {
        return bench_c_intsum_addr(xs)
    }
}

// Align64 is the first index of xs whose element sits on a 64-byte boundary.
export Align64(xs: []i64): int {
    r := Cast[int](Addr(xs) & 63)
    if r == 0 {
        return 0
    }
    return (64 - r) / 8
}
