// Why: printing, comparison, ordering, hashing and indexing are named by the
// language itself (Print holes, ==, sort, maps, xs[i]), so their interfaces
// belong in the prelude.
//
// What: the interfaces Format, Eq, Ord, Hash and Slice[T].
//
// How: declarations only. Slice[T] is the compiler-known interface of
// indexable types: []T, [N]T, List and HeapList implement it through Items
// and ItemsMut, and every Slice[T] method works on each of them.

/// Writes a value's text to a Writer, for a {hole} in Print, Arena.Fmt,
/// System.Print and every function shaped like Print.
/// The numbers, str, bool, char and references to them implement it, and so
/// do ?T, []T, List[T] and HeapList[T] when T does: an optional prints
/// some(v) or none, a sequence [a, b, c] or [], each element through its own
/// Format, so a str inside prints without quotes. An error whose payload
/// fields all have Format has it too: it prints its name, then its fields as
/// name: value in declaration order, joined by ", " in parentheses, as
/// NotFound(id: 3); a module's error prints unqualified. An error set has
/// Format when all its members do, and prints the member it holds.
export interface Format {
    Format(&self, w: mut Writer): WriteError!void
}

/// Equality of two values of one type, as Has, Index and map keys need it.
/// &T is Eq when T is, and compares the referents, not the addresses. On
/// floats it is IEEE ==, as the operator is, so a NaN equals nothing,
/// itself included.
export interface Eq {
    Eq(&self, other: &Self): bool
}

/// A strict weak order: Less is true when self comes before other. &T is Ord
/// when T is, and orders the referents. On floats it is IEEE <, so a NaN is
/// not ordered; sorting a slice that holds one leaves its elements in some
/// order, with no panic.
export interface Ord {
    Less(&self, other: &Self): bool
}

/// Feeds a value into a Hasher; values that are Eq must hash the same. &T is
/// Hash when T is, and hashes the referent. The floats have no Hash, so no
/// float is a Map or Set key.
export interface Hash {
    Hash(&self, h: mut Hasher)
}

/// The interface of indexable types: []T, [N]T, List and HeapList. Every
/// &self method of Slice[T] works on each of them. A mut self method needs a
/// mut []T, a var array, a List or a HeapList: other receivers are E0507,
/// and a local that is not var is E0508.
export interface Slice[T] {
    Items(&self): []T
    ItemsMut(mut self): mut []T
}
