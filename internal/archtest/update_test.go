// Why: go test ./... -update must work in every package with tests.
// What: an -update flag for this package, which has no golden files.
// How: registers the flag and ignores it.

package archtest

import "flag"

var _ = flag.Bool("update", false, "no golden files in this package")
