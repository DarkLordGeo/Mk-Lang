// Why: MIR is the contract between lower and every later stage (borrow,
// eval, mono, codegen), so it lives in its own package with data and
// validation only, no pass logic.
//
// What: the package documentation of internal/mir.
//
// How: mir.go and body.go hold the node types, validate*.go the
// invariants, print.go the text form, asm.go and tailcall.go the inline
// asm statement and the tail call.

// Package mir is the typed control-flow graph that borrow, eval, mono and
// codegen read and lower writes: Body with its Locals, Blocks, Statements and
// Terminators; Place and Projection; Operand (copy, move, interned const,
// scalar immediate); Rvalue; calls with error edges (ErrorEdge), argument
// modes and spawns; exit kinds with their two predicates (RunsOnerr,
// IsE0411Exit); cleanup blocks copied per exit edge (BlockOrigin); loops and
// defers.
//
// Every node is a pointer-free tagged struct; variable-length parts are
// Ranges into the Body's side slices (Projections, Operands, Args, Cases,
// GenericArgs). MIR IDs index one Body; their "none" is all ones because slot
// 0 of each is meaningful (local 0 is the return place, block 0 the entry).
//
// Body.Validate checks the invariants M1 to M13 (M13: inline asm); Printer
// gives a deterministic text form; Body.CFGChecksum is the block-structure
// hash PGO matches on. MIR is frozen per compiler version: fuel is defined
// against it.
package mir
