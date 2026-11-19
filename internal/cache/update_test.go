// Why: go test ./... -update passes the flag to every package with tests.
// What: an -update flag that does nothing.
// How: this package has no golden files, so it only accepts the flag.

package cache

import "flag"

var _ = flag.Bool("update", false, "no golden files in this package")
