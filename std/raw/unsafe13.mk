// Why: some low-level code needs a function pointer from a raw address, and
// copies or fills whose element accesses are each atomic.
// What: FnFromAddr, AtomicCopy and AtomicFill.
// How: bodiless intrinsics: codegen lowers each by its stable key and the
// evaluator runs or refuses it (internal/eval/intrinsic_machine.go). Each needs
// an unsafe context (E1101).

/// FnFromAddr is the function at addr, as the fn type F. addr must be the
/// address of code with F's signature, such as an entry of a std.vm code
/// region. Needs unsafe. It cannot run at compile time.
export FnFromAddr[F](addr: uint): F
/// AtomicCopy copies n elements from src to dst, each element read and
/// written whole, as a relaxed atomic access. T is a plain type of 1, 2, 4
/// or 8 bytes, aligned to its size (E0701 otherwise). The ranges may
/// overlap, as Move's may. O(n). Needs unsafe.
export AtomicCopy[T](dst: *T, src: *T, n: int)
/// AtomicFill writes x to the n elements at dst, each element written
/// whole, as a relaxed atomic access. T is as for AtomicCopy. O(n). Needs
/// unsafe.
export AtomicFill[T](dst: *T, x: T, n: int)
