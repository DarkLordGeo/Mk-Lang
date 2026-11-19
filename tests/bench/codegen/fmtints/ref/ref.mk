// The C side of the codegen kernel fmtints (../fmtints.c), compiled by clang
// 21 into a native object that tools/bench links into this program (README
// "The codegen table"), and the 64-byte alignment of the Mk buffers.

extern bench_c_fmtints_fill(seed: u64, n: i64)
extern bench_c_fmtints_run(n: i64): i64
extern bench_c_fmtints_addr64(xs: []i64): u64
extern bench_c_fmtints_addr8(xs: []u8): u64

export CFill(seed: u64, n: int) {
    unsafe "bench_c_fmtints_fill is in fmtints.c, linked into the benchmark build; it writes only its own static buffers, n is at most their capacity (checked by Main)" {
        bench_c_fmtints_fill(seed, Cast[i64](n))
    }
}

export CRun(n: int): int {
    unsafe "bench_c_fmtints_run is in fmtints.c; it reads and writes only its own static buffers" {
        return Cast[int](bench_c_fmtints_run(Cast[i64](n)))
    }
}

// Addr64 and Addr8 are the data address of xs as an integer.
export Addr64(xs: []i64): u64 {
    unsafe "bench_c_fmtints_addr64 returns the slice's data address as an integer and touches no memory" {
        return bench_c_fmtints_addr64(xs)
    }
}

export Addr8(xs: []u8): u64 {
    unsafe "bench_c_fmtints_addr8 returns the slice's data address as an integer and touches no memory" {
        return bench_c_fmtints_addr8(xs)
    }
}

// Align64 and Align8 are the first index of xs whose element sits on a
// 64-byte boundary.
export Align64(xs: []i64): int {
    r := Cast[int](Addr64(xs) & 63)
    if r == 0 {
        return 0
    }
    return (64 - r) / 8
}

export Align8(xs: []u8): int {
    r := Cast[int](Addr8(xs) & 63)
    if r == 0 {
        return 0
    }
    return 64 - r
}
