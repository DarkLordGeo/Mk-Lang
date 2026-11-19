// Why: go test ./... -update must work in every package with tests.
// What: the -update flag; this package has no golden files.
// How: declares the flag and ignores it.

package source

import "flag"

var _ = flag.Bool("update", false, "no golden files in this package")
