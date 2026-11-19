// Why: a spin-wait loop should tell the CPU it is spinning, to save power
// and yield to a sibling hyperthread.
//
// What: Pause, the spin-wait hint.
//
// How: bodiless, so check marks it an intrinsic: codegen lowers it by its
// stable key and the evaluator runs it as nothing
// (internal/eval/intrinsic_machine.go). Safe, O(1), allocates nothing.

/// Tells the CPU that the thread is spinning on a word, which saves power
/// and lets a sibling hardware thread run (`pause` on x86_64). It changes
/// nothing a program can observe. O(1); it allocates nothing.
export Pause()
