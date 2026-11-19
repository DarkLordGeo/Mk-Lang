// Why: some tests of the standard library need the compiler's own view of
// it (check's answers, resolved bodies), which package std cannot import.
//
// What: the package doc of stdtest; the tests live in its _test.go files.
//
// How: stdtest sits above driver, check and resolve in the package DAG and
// exports nothing, so its tests may import them without forming a cycle.

// Package stdtest holds the Go tests that need the compiler's view of the
// standard library: mk test over std's own test blocks
// (TestStdMkTests), the time to check std (BenchmarkStdCheck), and the
// tests that read check's own answers for std: known items, layouts and
// kinds, the layers of the resolved bodies (TestLayerZeroFiles) and
// the computed result sources (TestComputedSources). Tests that
// need only the mk command stay in package std.
//
// It is test-only: this file is its one non-test file, and it exports
// nothing. Its tests may start wsl.exe, so `go test ./internal/stdtest`
// runs under tools/wsl-lock.ps1 with -p 1: parallel WSL clients hang WSL.
package stdtest
