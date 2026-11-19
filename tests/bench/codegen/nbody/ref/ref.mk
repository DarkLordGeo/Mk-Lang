// The C side of the codegen kernel nbody (../nbody.c), compiled by clang 21
// into a native object that tools/bench links into this program (README "The
// codegen table").

extern bench_c_nbody_fill()
extern bench_c_nbody_run(steps: i64): i64

export CFill() {
    unsafe "bench_c_nbody_fill is in nbody.c, linked into the benchmark build; it writes only its own static state" {
        bench_c_nbody_fill()
    }
}

export CRun(steps: int): i64 {
    unsafe "bench_c_nbody_run is in nbody.c; it reads and writes only its own static state" {
        return bench_c_nbody_run(Cast[i64](steps))
    }
}
