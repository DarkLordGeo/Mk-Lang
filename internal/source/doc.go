// Why: every compiler package needs positions, file text, interned names,
// stable hashes and one way to report internal errors; they live at the
// bottom of the package DAG so everything can import them.
//
// What: the package documentation of internal/source.
//
// How: the package holds data types and primitives only, no compiler logic;
// line endings are normalized once, files are ordered by path bytes, and
// nothing is ordered or hashed by an ID that depends on goroutine timing.

// Package source is the bottom of the compiler's package DAG. It imports nothing
// internal and holds only data types, constructors, accessors, printing and
// hashing primitives:
//
//   - FileID, Pos, Span and LineCol: byte offsets into one file's normalized
//     text, 1-based lines and 1-based byte columns.
//   - File and SourceMap: normalized source text (BOM stripped, CRLF turned into
//     LF exactly once) with its line table and content hash.
//   - Symbol and Interner: the concurrency-safe identifier interner, with the
//     predeclared symbols of the language at fixed numbers.
//   - ICE, ICEf, Assertf, Guard and InternalError: the one mechanism for
//     internal invariant violations. A grep for "ICEf(" finds every raise site.
//   - IndexMap and IndexSet: insertion-ordered collections, the only maps whose
//     iteration may reach output.
//   - Hash, Hash128 and Hasher: the stable SHA-256 hasher over a canonical,
//     length-prefixed encoding.
//   - AppendList: the append-only, chunked list whose published elements are
//     read without a lock; the storage of every concurrently grown table.
//
// Conventions every type here follows: the zero value of an ID type means
// "none", and index 0 of every table is reserved. ID numbers (FileID, Symbol)
// follow insertion order, which may follow goroutine timing, so output is never
// ordered by them and they are never fed to a Hasher.
package source
