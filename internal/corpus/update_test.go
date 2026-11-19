// Why: go test ./... -update must work across the module, so this package
// declares the flag too.
// What: the -update flag.
// How: it rewrites the per-code snapshots under tests/conformance/codes/
// (TestSnapshots, TestE0000_Snapshot) and testdata/derive_eq.golden
// (TestDeriveEqReadsLikeHandwritten); read the diff before keeping it.

package corpus

import "flag"

var update = flag.Bool("update", false, "rewrite the tests/conformance/codes snapshots (TestSnapshots, TestE0000_Snapshot) and testdata/derive_eq.golden (TestDeriveEqReadsLikeHandwritten)")
