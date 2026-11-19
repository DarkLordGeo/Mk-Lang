// Why: every later stage (check, gen, the tools) must agree on what a name
// means, so one package decides it, once, before type checking.
//
// What: the package comment only.
//
// How: resolution runs one module at a time, over the module's parsed
// files, its imports' scopes, the prelude and the universe.

// Package resolve is the one producer of "what a name means". It
// declares every entity of a module as a types.Item with its stable key,
// resolves every name root to a Ref in per-file side tables, decides the
// type-or-expression slots the parser leaves open, and reports the
// names-phase diagnostics (E0201, E0204, E0206, E0207, E0609, E1209, E1221 and
// the E0101 forms it decides) and the unused-name E0208 in the types phase.
// Member lookup after a "." on a value or a type is check's; this package
// exports the folding and synonym helpers check reuses (Fold, Lookup,
// Synonyms).
package resolve
