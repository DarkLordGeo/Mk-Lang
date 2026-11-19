// Why: every pass reports errors, and the driver, the editor and the corpus all
// read them; one shared shape for a diagnostic keeps them from each inventing
// their own.
//
// What: the package documentation of diag, the contract package every pass
// emits diagnostics through.
//
// How: diag is data only: codes, phases, facts, fixes and the ordering and
// rendering of a finished report. Its one internal import is source, for spans.

// Package diag holds the diagnostic contract every pass emits through:
//
//   - Code, the named constants E0000 .. E1223, and the error catalogue of
//     doc/errors.md as a table (Catalogue, Code.Name).
//   - Phase and DefaultPhase: the report groups of the earliest-phase rule.
//   - Diagnostic, Fact, Fix, Edit and Cause: one error as plain data, built with
//     New and chained setters. FromInternal and Toolchain build E0000 and E0001.
//   - JSONWriter: a hand-written RFC 8259 writer with keys in schema order.
//   - Order and Report: the phase rule, the total sort, deduplication, root
//     resolution and the cap, then WriteJSON (one JSON envelope per build)
//     and Text.
//
// Nothing here runs a pass. A pass builds Diagnostics; the driver collects
// them from every goroutine, calls Order once, and prints the Report.
package diag
