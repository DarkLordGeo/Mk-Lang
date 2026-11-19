// The C side of the codegen kernel sieve (../sieve.c), compiled by clang 21
// into a native object that tools/bench links into this program (README "The
// codegen table"), and the 64-byte alignment of the Mk buffer.

extern bench_c_sieve_fill(n: i64)
extern bench_c_sieve_run(n: i64): i64
extern bench_c_sieve_addr(xs: []u8): u64

export CFill(n: int) {
    unsafe "bench_c_sieve_fill is in sieve.c, linked into the benchmark build; it writes only its own static buffer, n + 1 is at most its capacity (checked by Main)" {
        bench_c_sieve_fill(Cast[i64](n))
    }
}

export CRun(n: int): int {
    unsafe "bench_c_sieve_run is in sieve.c; it reads and writes only its own static buffer" {
        return Cast[int](bench_c_sieve_run(Cast[i64](n)))
    }
}

// Addr is the data address of xs as an integer.
export Addr(xs: []u8): u64 {
    unsafe "bench_c_sieve_addr returns the slice's data address as an integer and touches no memory" {
        return bench_c_sieve_addr(xs)
    }
}

// Align64 is the first index of xs whose element sits on a 64-byte boundary.
export Align64(xs: []u8): int {
    r := Cast[int](Addr(xs) & 63)
    if r == 0 {
        return 0
    }
    return 64 - r
}
