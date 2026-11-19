// Why: a Linux case that overruns must end inside the VM, or it can keep
// holding WSL after the Windows client is killed.
// What: TestBoundInVM, the command-line rewrite of boundInVM.
// How: compares boundInVM's argv and limit for a normal case, a fraction of
// a second left, and a command without -e.

package main

import (
	"slices"
	"testing"
)

// TestBoundInVM: a Linux case's command gains `timeout -k 5 <s>` right
// after -e, so a case that runs over is ended inside the VM, not only by
// killing the Windows wsl.exe client.
func TestBoundInVM(t *testing.T) {
	got, s := boundInVM([]string{`C:\wsl.exe`, "--cd", "w", "-e", "/mnt/v/x/prog", "--flag"}, 29.7)
	want := []string{`C:\wsl.exe`, "--cd", "w", "-e", "timeout", "-k", "5", "29", "/mnt/v/x/prog", "--flag"}
	if s != 29 || !slices.Equal(got, want) {
		t.Errorf("boundInVM = %q, %d; want %q, 29", got, s, want)
	}
	if got, s := boundInVM([]string{"wsl.exe", "-e", "p"}, 0.2); s != 1 || got[5] != "1" {
		t.Errorf("a fraction of a second left: %q, %d; want at least 1", got, s)
	}
	if got, s := boundInVM([]string{"mk", "test", "--sim"}, 30); s != 0 || len(got) != 3 {
		t.Errorf("no -e: %q, %d; want unchanged, 0", got, s)
	}
}
