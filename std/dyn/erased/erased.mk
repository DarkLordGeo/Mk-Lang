// Why: std.dyn's Ref must tell types apart at run time, and the compiler is
// the only place that knows a type's identity.
//
// What: Key, the type key that std.dyn's Ref stores and compares.
//
// How: Key stays in a module of its own because codegen and eval fold it by
// its item key `std.dyn.erased.Key` (internal/codegen/typekey.go,
// internal/eval/typekey.go). No import, no allocation, no Io.

// Bodiless, so an intrinsic.
/// Key is T's type key: a compile-time constant, different for each
/// distinct type of one program. If two types ever met one key, the
/// compiler would stop with an internal error naming both, rather than
/// build the program. O(1).
export Key[T](): u64
