// Why: std-x509_constraints_test.go and std-x509_regress_test.go name Windows-only
// helpers of std-x509_platform_test.go and rt_cert_test.go, and must compile
// off Windows.
// What: stubs of x509GtsRootR1, x509CachedRecord and rt8StoreRoots.
// How: the callers skip off Windows before calling them.

//go:build !windows

package std

import (
	"testing"
	"time"
)

// x509GtsRootR1 is GTS Root R1's SHA-1 thumbprint.
const x509GtsRootR1 = "e58c1cc4913b38634be9106ee3ad8e6b9dd9814a"

func x509CachedRecord(now time.Time, host string, chain [][]byte) string {
	panic("x509CachedRecord: the platform verifier is Windows only")
}

func rt8StoreRoots(t *testing.T) [][]byte {
	t.Fatal("rt8StoreRoots: the ROOT store is Windows only")
	return nil
}
