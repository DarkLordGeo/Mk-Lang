// Why: fine-grained timing needs the CPU's cycle counter, which only the
// compiler can reach.
// What: runtime.Cycles.
// How: bodiless, so check marks it an intrinsic: codegen lowers it by its
// stable key and the evaluator runs it (internal/eval/intrinsic_machine.go).
// Safe, O(1), allocates nothing.

/// Reads the CPU's cycle counter: `rdtsc` on x86_64, `CNTVCT_EL0` on
/// aarch64. It never decreases on one thread. Its rate is the target's: on
/// x86_64 the time-stamp counter's, on aarch64 the generic timer's fixed
/// frequency (`CNTFRQ_EL0`), not the processor's clock. It cannot run at
/// compile time.
export Cycles(): u64
