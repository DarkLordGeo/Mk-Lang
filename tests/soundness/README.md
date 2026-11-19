# tests/soundness

The records of the dynamic soundness checker, the program generator and the miscompile oracle, which test the guarantees of `doc/memory.md`. No Go file lives here; the tests in `internal/corpus` read these files. This directory is not a corpus root: `mk corpus` never reads it.

| Path | Holds |
|---|---|
| `header.mk` | The generator's helper header. It is a copy of `internal/corpus/soundheader.go`'s `soundHeader`; `TestSoundGen_HeaderFile` keeps the two equal and `-update` rewrites this file |
| `mutants.txt`, `mutants-flows8.txt`, `mutants-fld3.txt` | The mutant tables of the mutation test: one block per mutant, applied to one file of `internal/borrow` or `internal/check` only through `go test -overlay`. A row's `witness` line names the witnesses it must flip |
| `witness/` | The compiled witnesses: one-file programs and package directories (`pkg.mk` and `main.mk`), each judged by its verdict and markers (`TestFlowsWitnesses`) |
| `found/open/` | Violations (`F<id>.mk`) and compiler crashes on generated programs (`C<id>.mk`) not yet closed. `TestSound_FoundOpen` requires each to still reproduce |
| `found/closed/` | Closed ones, each a one-file corpus unit judged by its verdict (`TestSound_FoundClosed`) |
| `miscompile/open/` | Oracle differences (`M<id>.mk`) not yet closed, each naming the package whose fix closes it |
| `miscompile/closed/` | Closed ones; the oracle reruns them |
| `runs/` | One JSON record per sized run, throughput measurement, mutation run, rejection sample and long oracle run |

A record's id is the first 12 hex digits of the SHA-256 of its program text without its leading `//` lines and its `// <- Exxxx` markers, so closing a record keeps its name.

**Closing a found record.** The runner writes `// needs: unknown`. Triage names the rule the program needs: a wrong rule implementation is a bug in `internal/borrow`; a missing or wrong rule is a change to the memory model in `doc/memory.md`. Never special-case anything to make a record pass. The fix moves the file to `found/closed/`, replaces `// verdict: ok` by the verdict the fixed compiler gives (with `// <- Exxxx` on each reported line), and adds `// closed: <the rule change>` (and `// case: Hnn` when a hard case was added).

**Closing a miscompile record.** The fix moves the file to `miscompile/closed/` with `// closed: <the change>`.

`found/open/` and `miscompile/open/` must be empty for a release.
