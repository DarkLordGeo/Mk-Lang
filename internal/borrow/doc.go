// Why: memory safety without a garbage collector rests on one pass that
// knows, for every function body, which values are initialized, moved or
// borrowed at every point, and rejects every access that conflicts.
//
// What: the package documentation of internal/borrow, the borrow checker.
//
// How: the files split the checker by concern: setup.go builds the per-body
// roots and loans, transfer.go and call.go are the transfer of statements
// and calls, access.go and store.go judge accesses and stores, exits.go the
// function boundary, solve.go drives the fixpoint and the replay, and
// report.go orders and deduplicates the diagnostics.

// Package borrow is stage 7 of the pipeline: for one MIR body it decides the
// init, move and linear state of every root, the loans every root holds,
// every conflict between an access and a live loan, every store into memory a
// parameter or a global reaches, and every function-boundary fact. It reads
// only the body, signatures and type facts, never another body, so every body
// is an independent job. The rules it enforces are those of doc/memory.md.
//
// The check is one forward dataflow over blocks: a fixpoint that runs
// transfers only, then one replay of every reachable block from its final
// entry state with every check on. Sets of loans, roots and sites are
// hash-consed and immutable; states are persistent vectors of chunks copied
// on write; both are per body and discarded when Check returns.
package borrow
