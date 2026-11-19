// The C side of the codegen kernel matmul (../matmul.c), compiled by clang
// 21 into a native object that tools/bench links into this program (README
// "The codegen table"), and the 64-byte alignment of the Mk buffers.

extern bench_c_matmul_fill(seed: u64, n: i64)
extern bench_c_matmul_run(n: i64): i64
extern bench_c_matmul_addr(xs: []f64): u64

export CFill(seed: u64, n: int) {
    unsafe "bench_c_matmul_fill is in matmul.c, linked into the benchmark build; it writes only its own static buffers, n is at most 512 (checked by Main)" {
        bench_c_matmul_fill(seed, Cast[i64](n))
    }
}

export CRun(n: int): i64 {
    unsafe "bench_c_matmul_run is in matmul.c; it reads and writes only its own static buffers" {
        return bench_c_matmul_run(Cast[i64](n))
    }
}

// Addr is the data address of xs as an integer.
export Addr(xs: []f64): u64 {
    unsafe "bench_c_matmul_addr returns the slice's data address as an integer and touches no memory" {
        return bench_c_matmul_addr(xs)
    }
}

// Align64 is the first index of xs whose element sits on a 64-byte boundary.
export Align64(xs: []f64): int {
    r := Cast[int](Addr(xs) & 63)
    if r == 0 {
        return 0
    }
    return (64 - r) / 8
}
