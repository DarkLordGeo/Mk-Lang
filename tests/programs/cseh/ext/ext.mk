extern seh_run(which: i32): u64

/// Run is the C source's case which (lib/seh.c's seh_run).
export Run(which: i32): u64 {
    unsafe "seh_run is the package's C; it handles its own faults" {
        return seh_run(which)
    }
}
