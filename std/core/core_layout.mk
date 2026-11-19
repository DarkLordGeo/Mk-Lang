// Why: generic code sometimes needs a type parameter's size and alignment
// under older names that callers still use.
//
// What: SizeOfT[T]() and AlignOfT[T]().
//
// How: the bodies are the language's own operators SizeOf[T]() and
// AlignOf[T](); the names stay so that no caller moves, and new code writes
// the operators directly.

/// The size of T in bytes, the same as the SizeOf[T]() operator, which code
/// should prefer. O(1), a constant.
export SizeOfT[T](): int {
    return SizeOf[T]()
}

/// The alignment of T in bytes, the same as the AlignOf[T]() operator, which
/// code should prefer. O(1), a constant.
export AlignOfT[T](): int {
    return AlignOf[T]()
}
