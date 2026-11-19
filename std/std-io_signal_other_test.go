//go:build !windows

// Why: the signal test's Windows half raises console control events, which
// only Windows has; package std's tests run on a Windows host.
// What: nothing; the stub that keeps the package building elsewhere.
// How: Linux is reached through WSL (lib146Linux in std-io_signal_test.go).

package std

import "testing"

func lib146Windows(t *testing.T, exe string) {
	t.Skip("console control events need a Windows host")
}
