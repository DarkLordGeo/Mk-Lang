// Why: layout-aware code needs a field's byte offset, and hot loops want to
// prefetch memory they will touch soon; neither can be written in Mk.
//
// What: OffsetOf, a field's offset as a compile-time value, and Prefetch, a
// read or write prefetch hint.
//
// How: each is bodiless, so check marks it an intrinsic: codegen lowers it
// by its stable key and the evaluator runs it
// (internal/eval/intrinsic_machine.go). Each is safe, O(1), and allocates
// nothing.

/// The byte offset of the field named field in T's layout, as a
/// compile-time value; a field T lacks is E0901. It allocates nothing.
export OffsetOf[T](field: str): int
/// A hint that p's memory will soon be read, or written when write is
/// true; it changes nothing a program can observe. O(1); allocates
/// nothing.
export Prefetch[T](p: &T, write: bool)
